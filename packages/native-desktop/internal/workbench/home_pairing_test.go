package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/skip2/go-qrcode"
)

func syntheticPairingLink(size int) string {
	return "https://app.paseo.sh/#offer=" + strings.Repeat("A", size)
}

// The dialog must read the daemon's actual response payload: the deep link and
// the relay flag matter, the terminal-art qr field does not.
func TestRequestPairingOfferDecodesDaemonPayload(t *testing.T) {
	link := syntheticPairingLink(96)
	caller := directoryCaller(func(_ context.Context, typ string, payload json.RawMessage) (json.RawMessage, error) {
		if typ != "daemon.get_pairing_offer.request" {
			t.Fatalf("request type = %q", typ)
		}
		if string(payload) != "{}" {
			t.Fatalf("request payload = %s", payload)
		}
		body, err := json.Marshal(map[string]any{
			"requestId":    "r1",
			"url":          " " + link + " ",
			"qr":           "\x1b[47m\x1b[30m██\x1b[0m",
			"relayEnabled": true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return body, nil
	})
	offer, err := requestPairingOffer(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if offer.URL != link {
		t.Fatalf("url = %q", offer.URL)
	}
	if !offer.RelayEnabled || offer.QR != nil || offer.QRError != "" {
		t.Fatalf("offer = %+v", offer)
	}
}

func TestRequestPairingOfferKeepsRelayDisabledOffer(t *testing.T) {
	caller := directoryCaller(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"requestId":"r1","url":"","qr":null,"relayEnabled":false}`), nil
	})
	offer, err := requestPairingOffer(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if offer.URL != "" || offer.RelayEnabled {
		t.Fatalf("offer = %+v", offer)
	}
}

func TestRequestPairingOfferPropagatesDaemonError(t *testing.T) {
	caller := directoryCaller(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, &client.RPCError{RequestType: "daemon.get_pairing_offer.request", Message: "relay disabled"}
	})
	if _, err := requestPairingOffer(context.Background(), caller); err == nil || !strings.Contains(err.Error(), "relay disabled") {
		t.Fatalf("err = %v", err)
	}
}

func TestDecodePairingOfferRejectsUnusableLinks(t *testing.T) {
	cases := map[string]string{
		"missing offer fragment": `{"url":"https://app.paseo.sh/","relayEnabled":true}`,
		"empty offer":            `{"url":"https://app.paseo.sh/#offer=","relayEnabled":true}`,
		"not a web link":         `{"url":"app.paseo.sh/#offer=abc","relayEnabled":true}`,
		"wrong scheme":           `{"url":"ftp://app.paseo.sh/#offer=abc","relayEnabled":true}`,
		"not json":               `{"url":42}`,
	}
	for name, raw := range cases {
		if _, err := decodePairingOffer(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	if _, err := decodePairingOffer(json.RawMessage(`{"url":"","relayEnabled":true,"extra":{"ignored":1}}`)); err != nil {
		t.Fatalf("relay-enabled offer without a link: %v", err)
	}
}

// An offer from a dialog that was closed or reopened must never land in the
// current dialog, even if it arrives after a newer load started.
func TestHomePairingDropsStaleLifetimes(t *testing.T) {
	var st homePairingState
	st.open = true
	const gen = uint64(7)
	first := st.begin(gen)
	if !st.loading(gen, first) {
		t.Fatal("current lifetime rejected its own load")
	}
	second := st.begin(gen)
	if first == second {
		t.Fatal("reopen reused the epoch")
	}
	if !st.loading(gen, second) {
		t.Fatal("reopened lifetime rejected its own load")
	}
	stale := homePairingOffer{URL: syntheticPairingLink(32), RelayEnabled: true}
	if st.complete(gen, first, stale) {
		t.Fatal("closed dialog accepted a stale offer")
	}
	if st.offer.URL != "" || !st.busy || st.loaded {
		t.Fatalf("stale result changed state: %+v", st)
	}
	if st.complete(gen+1, second, stale) {
		t.Fatal("offer from another host generation was accepted")
	}
	current := homePairingOffer{URL: syntheticPairingLink(48), RelayEnabled: true}
	if !st.complete(gen, second, current) || st.offer.URL != current.URL || st.busy || !st.loaded {
		t.Fatalf("current offer not applied: %+v", st)
	}
	// A failed reload clears the previous link rather than leaving it on show.
	if !st.fail(gen, second, "Host is not connected") || st.offer.URL != "" || st.loaded || st.err != "Host is not connected" {
		t.Fatalf("failure kept the old offer: %+v", st)
	}
	// Relay work belongs to its lifetime too.
	if st.relayPending(gen, first) {
		t.Fatal("stale lifetime started relay work")
	}
	if !st.relayPending(gen, st.epoch) || !st.relayFinished(gen, st.epoch, "") {
		t.Fatalf("relay state: %+v", st)
	}
}

func TestHomePairingFailureTextNamesDisconnection(t *testing.T) {
	if got := pairingFailure(client.ErrDisconnected); got != "Host is not connected" {
		t.Fatalf("disconnect = %q", got)
	}
	if got := pairingFailure(errors.New("boom")); got != "boom" {
		t.Fatalf("other = %q", got)
	}
	if got := pairingFailure(nil); got != "" {
		t.Fatalf("nil = %q", got)
	}
}

func TestDaemonPairingFeatures(t *testing.T) {
	pairing, relay := daemonPairingFeatures(json.RawMessage(`{"serverId":"s","features":{"daemonStatusRpc":true,"relayConfig":true}}`))
	if !pairing || !relay {
		t.Fatalf("capabilities = %v %v", pairing, relay)
	}
	pairing, relay = daemonPairingFeatures(json.RawMessage(`{"features":{"daemonStatusRpc":true}}`))
	if !pairing || relay {
		t.Fatalf("older daemon capabilities = %v %v", pairing, relay)
	}
	pairing, relay = daemonPairingFeatures(json.RawMessage(`{"features":{"relayConfig":true}}`))
	if pairing || !relay {
		t.Fatalf("partial capabilities = %v %v", pairing, relay)
	}
	if pairing, relay = daemonPairingFeatures(nil); pairing || relay {
		t.Fatal("no serverInfo advertised pairing")
	}
	if pairing, relay = daemonPairingFeatures(json.RawMessage(`{"features":"x"}`)); pairing || relay {
		t.Fatal("malformed serverInfo advertised pairing")
	}
}

func TestConfirmRelayEnabledReadsConfigResponse(t *testing.T) {
	if err := confirmRelayEnabled(json.RawMessage(`{"requestId":"r","config":{"relay":{"enabled":true},"mcp":{"injectIntoAgents":true}}}`)); err != nil {
		t.Fatal(err)
	}
	if err := confirmRelayEnabled(json.RawMessage(`{"requestId":"r","config":{"relay":{"enabled":false}}}`)); err == nil {
		t.Fatal("disabled relay reported success")
	}
	if err := confirmRelayEnabled(json.RawMessage(`{"config":`)); err == nil {
		t.Fatal("malformed config reported success")
	}
}

// The rendered bitmap must scan: the quiet zone is blank, modules are drawn at
// one whole number of pixels each, and every module matches the encoder's.
func TestPairingQRImageRendersScannableBitmap(t *testing.T) {
	for _, link := range []string{syntheticPairingLink(24), syntheticPairingLink(300)} {
		img, err := pairingQRImage(link)
		if err != nil {
			t.Fatal(err)
		}
		code, err := qrcode.New(link, qrcode.Medium)
		if err != nil {
			t.Fatal(err)
		}
		modules := code.Bitmap()
		n := len(modules)
		bounds := img.Bounds()
		if bounds.Dx() != bounds.Dy() || bounds.Dx() < n {
			t.Fatalf("image %v for %d modules", bounds, n)
		}
		scale := bounds.Dx() / n
		if scale < 1 || scale*n != bounds.Dx() {
			t.Fatalf("scale %d does not divide %d modules into %d pixels", scale, n, bounds.Dx())
		}
		if modules[0][0] || modules[0][n-1] {
			t.Fatal("encoder produced no quiet zone to preserve")
		}
		assertWhite := func(x, y int) {
			r, g, b, _ := img.At(x, y).RGBA()
			if r != 0xffff || g != 0xffff || b != 0xffff {
				t.Fatalf("quiet zone pixel (%d,%d) is not white", x, y)
			}
		}
		for y := range bounds.Dy() {
			for x := range bounds.Dx() {
				if x >= 4*scale && x < bounds.Dx()-4*scale && y >= 4*scale && y < bounds.Dy()-4*scale {
					continue
				}
				assertWhite(x, y)
			}
		}
		for y, row := range modules {
			for x, dark := range row {
				r, _, _, _ := img.At(x*scale+scale/2, y*scale+scale/2).RGBA()
				if got := r < 0x8000; got != dark {
					t.Fatalf("module (%d,%d) drawn %v, want %v", x, y, got, dark)
				}
			}
		}
	}
}

func TestPairingQRImageRejectsEmptyLink(t *testing.T) {
	if _, err := pairingQRImage("  "); err == nil {
		t.Fatal("empty link rendered a code")
	}
}
