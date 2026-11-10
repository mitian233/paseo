package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"net/url"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
	"github.com/skip2/go-qrcode"
)

// Pair device mirrors packages/app/src/desktop/components/pair-device-section.tsx
// against the daemon's daemon.get_pairing_offer.request contract: the payload
// carries {url, qr, relayEnabled}, where url is the app deep link with the
// connection offer in its fragment and qr is the daemon's terminal artwork.
// The QR shown here is drawn from url as a bitmap instead, and the offer has
// no expiry in the protocol: it stays valid until the daemon changes its key
// or relay endpoint, so it is refreshed on demand and dropped when the dialog,
// host, or connection that produced it goes away.

const (
	pairingRelayDocsURL     = "https://paseo.sh/docs/security"
	pairingTailscaleDocsURL = "https://paseo.sh/docs/connectivity#tailscale"
	pairingOfferTimeout     = 20 * time.Second
	// pairingQRTargetPixels sizes the bitmap: each module becomes the same
	// whole number of pixels, aiming for at least this many pixels a side.
	pairingQRTargetPixels = 640
	// pairingQRDisplayDIPs is the width the tile shows the code at, close to
	// the 304 DIP tile of the phone app.
	pairingQRDisplayDIPs = 304
	pairingCopiedReset   = 2 * time.Second
)

type pairingCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

type homePairingOffer struct {
	URL          string
	RelayEnabled bool
	QR           ui.ImageSource
	QRError      string
}

type homePairingState struct {
	open, busy, enablingRelay, copied bool
	// epoch counts every open, close, and reload of the dialog; generation is
	// the host generation the state was started for. Async results carrying
	// any other pair are from a dialog that moved on and must be dropped.
	epoch, generation uint64
	loaded            bool
	offer             homePairingOffer
	err, relayErr     string
	clipboard         string
	// Capabilities are parsed once per connection instead of once per frame,
	// as the home tile asks whether pairing is available on every frame.
	featuresKnown, featuresPairing, featuresRelay bool
	featuresGen                                   uint64
}

// begin starts a new dialog lifetime for generation gen, dropping whatever an
// earlier lifetime loaded, and returns epoch to match its results against.
func (s *homePairingState) begin(gen uint64) uint64 {
	s.epoch++
	s.generation = gen
	s.busy = false
	s.enablingRelay = false
	s.copied = false
	s.loaded = false
	s.err = ""
	s.relayErr = ""
	s.clipboard = ""
	s.offer = homePairingOffer{}
	return s.epoch
}

// owns reports whether results tagged with gen and epoch still belong to this
// dialog lifetime.
func (s *homePairingState) owns(gen, epoch uint64) bool {
	return s.open && s.generation == gen && s.epoch == epoch
}

func (s *homePairingState) loading(gen, epoch uint64) bool {
	if !s.owns(gen, epoch) {
		return false
	}
	s.busy = true
	s.err = ""
	return true
}

func (s *homePairingState) complete(gen, epoch uint64, offer homePairingOffer) bool {
	if !s.owns(gen, epoch) {
		return false
	}
	s.busy = false
	s.err = ""
	s.loaded = true
	s.offer = offer
	return true
}

func (s *homePairingState) fail(gen, epoch uint64, message string) bool {
	if !s.owns(gen, epoch) {
		return false
	}
	s.busy = false
	s.loaded = false
	s.err = message
	s.offer = homePairingOffer{}
	return true
}

func (s *homePairingState) relayPending(gen, epoch uint64) bool {
	if !s.owns(gen, epoch) || s.enablingRelay {
		return false
	}
	s.enablingRelay = true
	s.relayErr = ""
	return true
}

func (s *homePairingState) relayFinished(gen, epoch uint64, message string) bool {
	if !s.owns(gen, epoch) {
		return false
	}
	s.enablingRelay = false
	s.relayErr = message
	return true
}

// openHomePairing opens the Pair device dialog and loads a current offer.
func (a *App) openHomePairing() {
	st := &a.desk.homePairing
	st.open = true
	st.begin(a.generation)
	switch {
	case a.client == nil || !a.connected:
		st.err = "Host is not connected"
	case !a.pairingSupported():
		st.err = "Update the host to pair a device."
	default:
		a.loadHomePairing()
	}
}

// closeHomePairing invalidates every load in flight and forgets the offer, so
// reopening fetches a current one rather than showing a stale link.
func (a *App) closeHomePairing() {
	st := &a.desk.homePairing
	st.epoch++
	st.open = false
	st.busy = false
	st.enablingRelay = false
	st.copied = false
	st.loaded = false
	st.offer = homePairingOffer{}
	st.err = ""
	st.relayErr = ""
	st.clipboard = ""
}

// homePairingAvailable reports whether the connected daemon advertises the
// pairing RPC this dialog needs. The phone app also requires its own embedded
// daemon, which a native window never hosts, so capability is the whole gate.
func (a *App) homePairingAvailable() bool {
	return a.connected && a.pairingSupported()
}

func (a *App) pairingSupported() bool {
	pairing, _ := a.pairingFeatures()
	return pairing
}

func (a *App) pairingCanConfigureRelay() bool {
	_, relayConfig := a.pairingFeatures()
	return relayConfig
}

// pairingFeatures reads the serverInfo capabilities the pairing UI gates on —
// daemonStatusRpc for the offer RPC, relayConfig for enabling relay from a
// client — and keeps them for the life of the connection. A disconnect drops
// them, so a reconnected daemon is asked again rather than trusted.
func (a *App) pairingFeatures() (pairing, relayConfig bool) {
	st := &a.desk.homePairing
	if !a.connected || a.client == nil {
		st.featuresKnown, st.featuresPairing, st.featuresRelay = false, false, false
		return false, false
	}
	info := a.client.ServerInfo()
	if len(info) == 0 {
		return false, false
	}
	if !st.featuresKnown || st.featuresGen != a.generation {
		st.featuresGen = a.generation
		st.featuresPairing, st.featuresRelay = daemonPairingFeatures(info)
		st.featuresKnown = true
	}
	return st.featuresPairing, st.featuresRelay
}

// daemonPairingFeatures reads the serverInfo capabilities the pairing UI gates
// on: daemonStatusRpc for the offer RPC, relayConfig for enabling relay from a
// client.
func daemonPairingFeatures(serverInfo json.RawMessage) (pairing bool, relayConfig bool) {
	var info struct {
		Features struct {
			DaemonStatusRPC bool `json:"daemonStatusRpc"`
			RelayConfig     bool `json:"relayConfig"`
		} `json:"features"`
	}
	if len(serverInfo) == 0 || json.Unmarshal(serverInfo, &info) != nil {
		return false, false
	}
	return info.Features.DaemonStatusRPC, info.Features.RelayConfig
}

// loadHomePairing requests a fresh offer for the current dialog lifetime and
// replaces whatever it showed. The request runs off the UI thread; only a
// result for the same host generation and dialog epoch is applied.
func (a *App) loadHomePairing() {
	st := &a.desk.homePairing
	if a.client == nil || !st.open {
		return
	}
	caller := a.client
	gen, epoch := a.generation, st.epoch
	if !st.loading(gen, epoch) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), pairingOfferTimeout)
		defer cancel()
		offer, err := requestPairingOffer(ctx, caller)
		var picture image.Image
		if err == nil && offer.URL != "" {
			// The link stays usable without a code, so render failures only
			// annotate the offer instead of failing it.
			if picture, err = pairingQRImage(offer.URL); err != nil {
				offer.QRError = err.Error()
				err = nil
			}
		}
		a.update(gen, func() {
			cur := &a.desk.homePairing
			if err != nil {
				cur.fail(gen, epoch, pairingFailure(err))
				return
			}
			if picture != nil {
				offer.QR = ui.NewBitmap(picture).Pixelated()
			}
			cur.complete(gen, epoch, offer)
		})
	}()
}

// enableHomePairingRelay turns the daemon relay on through the config patch
// the phone app uses, then reloads the offer so the QR carries a relay offer.
func (a *App) enableHomePairingRelay() {
	st := &a.desk.homePairing
	if a.client == nil || !a.connected || !a.pairingCanConfigureRelay() {
		return
	}
	caller := a.client
	gen, epoch := a.generation, st.epoch
	if !st.relayPending(gen, epoch) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), pairingOfferTimeout)
		defer cancel()
		raw, err := caller.Call(ctx, "set_daemon_config_request", json.RawMessage(`{"config":{"relay":{"enabled":true}}}`))
		if err == nil {
			err = confirmRelayEnabled(raw)
		}
		a.update(gen, func() {
			cur := &a.desk.homePairing
			if err != nil {
				cur.relayFinished(gen, epoch, pairingFailure(err))
				return
			}
			if !cur.relayFinished(gen, epoch, "") {
				return
			}
			a.loadHomePairing()
		})
	}()
}

func (a *App) copyHomePairingLink() {
	st := &a.desk.homePairing
	if st.offer.URL == "" {
		return
	}
	st.clipboard = st.offer.URL
	st.copied = true
	gen, epoch := a.generation, st.epoch
	time.AfterFunc(pairingCopiedReset, func() {
		a.update(gen, func() {
			if cur := &a.desk.homePairing; cur.epoch == epoch && cur.open {
				cur.copied = false
			}
		})
	})
}

func requestPairingOffer(ctx context.Context, caller pairingCaller) (homePairingOffer, error) {
	raw, err := caller.Call(ctx, "daemon.get_pairing_offer.request", json.RawMessage(`{}`))
	if err != nil {
		return homePairingOffer{}, err
	}
	return decodePairingOffer(raw)
}

// decodePairingOffer reads the response payload and keeps only what the dialog
// can use: the deep link and whether relay made pairing possible. The daemon's
// `qr` field is terminal artwork, so it is skipped rather than rendered.
func decodePairingOffer(raw json.RawMessage) (homePairingOffer, error) {
	var payload struct {
		URL          string `json:"url"`
		RelayEnabled bool   `json:"relayEnabled"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return homePairingOffer{}, fmt.Errorf("pairing offer: %w", err)
	}
	link := strings.TrimSpace(payload.URL)
	if link != "" {
		if err := validatePairingLink(link); err != nil {
			return homePairingOffer{}, err
		}
	}
	return homePairingOffer{URL: link, RelayEnabled: payload.RelayEnabled}, nil
}

// validatePairingLink accepts the app deep link the phone can open: an http(s)
// URL whose fragment carries the connection offer. Anything else would scan
// into a dead end.
func validatePairingLink(link string) error {
	u, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("pairing link: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return errors.New("pairing link is not an http(s) URL")
	}
	offer := strings.TrimPrefix(u.Fragment, "offer=")
	if offer == "" || offer == u.Fragment {
		return errors.New("pairing link carries no connection offer")
	}
	return nil
}

func confirmRelayEnabled(raw json.RawMessage) error {
	var payload struct {
		Config struct {
			Relay struct {
				Enabled bool `json:"enabled"`
			} `json:"relay"`
		} `json:"config"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("set_daemon_config_request: %w", err)
	}
	if !payload.Config.Relay.Enabled {
		return errors.New("The daemon did not enable relay.")
	}
	return nil
}

func pairingFailure(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, client.ErrDisconnected) {
		return "Host is not connected"
	}
	return err.Error()
}

// pairingQRImage renders link as a QR bitmap. qrcode's Bitmap already holds
// the four-module quiet zone, and every module is drawn at the same whole
// number of pixels so no module edge falls on a fractional pixel.
func pairingQRImage(link string) (image.Image, error) {
	link = strings.TrimSpace(link)
	if link == "" {
		return nil, errors.New("pairing link is empty")
	}
	code, err := qrcode.New(link, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("encode pairing QR: %w", err)
	}
	modules := code.Bitmap()
	size := len(modules)
	if size == 0 || len(modules[0]) != size {
		return nil, errors.New("pairing QR is not square")
	}
	scale := pairingQRTargetPixels / size
	if scale < 1 {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, size*scale, size*scale))
	white, black := image.NewUniform(color.White), image.NewUniform(color.Black)
	draw.Draw(img, img.Bounds(), white, image.Point{}, draw.Src)
	for y, row := range modules {
		for x, dark := range row {
			if !dark {
				continue
			}
			draw.Draw(img, image.Rect(x*scale, y*scale, (x+1)*scale, (y+1)*scale), black, image.Point{}, draw.Src)
		}
	}
	return img, nil
}

// homePairingDialog shows the dialog while the state is open and keeps it in
// step with the window: dismissing it closes the lifetime, and a disconnect
// drops the offer loaded for a connection that no longer exists.
func (a *App) homePairingDialog(c *ui.Context) {
	st := &a.desk.homePairing
	if !st.open {
		return
	}
	if st.clipboard != "" {
		c.WriteClipboard(st.clipboard)
		st.clipboard = ""
	}
	if st.generation != a.generation {
		// A reconnect or host switch moved on: earlier offers and loads
		// belong to a daemon this window is no longer talking to.
		st.begin(a.generation)
	}
	if !a.connected && st.offer.URL != "" {
		st.offer = homePairingOffer{}
		st.loaded = false
		st.busy = false
	}
	if a.connected && a.pairingSupported() && !st.loaded && !st.busy && st.err == "" {
		a.loadHomePairing()
	}
	open := st.open
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(480).Padding(24).Gap(12).Children(func() {
			ui.Text(c, "Pair a device").Bold()
			a.homePairingBody(c)
		})
	})
	if st.open && !open {
		a.closeHomePairing()
	}
}

func (a *App) homePairingBody(c *ui.Context) {
	st := &a.desk.homePairing
	muted := c.Theme().TextMuted
	switch {
	case !a.connected:
		a.homePairingError(c, "Host is not connected")
	case !a.pairingSupported():
		a.homePairingError(c, "Update the host to pair a device.")
	case st.err != "" && !st.busy:
		a.homePairingError(c, st.err)
	case !st.loaded:
		ui.Text(c, "Loading pairing offer…").TextColor(muted)
	case !st.offer.RelayEnabled:
		a.homePairingRelayConsent(c)
	case st.offer.URL == "":
		ui.Text(c, "Pairing offer unavailable.").TextColor(muted)
		a.homePairingButtons(c, false)
	default:
		a.homePairingOfferBody(c)
	}
}

func (a *App) homePairingError(c *ui.Context, message string) {
	st := &a.desk.homePairing
	ui.Text(c, message).TextColor(c.Theme().Danger)
	ui.Row(c).Gap(8).Children(func() {
		if a.connected && a.pairingSupported() {
			if ui.Button(c, "Retry").Disabled(st.busy).Clicked() {
				a.loadHomePairing()
			}
		}
		if ui.Button(c, "Close").Clicked() {
			a.closeHomePairing()
		}
	})
}

func (a *App) homePairingRelayConsent(c *ui.Context) {
	st := &a.desk.homePairing
	theme := c.Theme()
	canConfigure := a.pairingCanConfigureRelay()
	ui.Text(c, "Enable relay?").Bold()
	ui.Text(c, "Relay lets this device connect from anywhere. Pairing traffic is end-to-end encrypted.").TextColor(theme.TextMuted)
	ui.Link(c, "How relay works", pairingRelayDocsURL)
	if st.relayErr != "" {
		ui.Text(c, st.relayErr).TextColor(theme.Danger)
	}
	if !canConfigure {
		ui.Text(c, "Update the host to enable relay from Paseo Desktop.").TextColor(theme.Warning)
	}
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "Not now").Disabled(st.enablingRelay).Clicked() {
			a.closeHomePairing()
		}
		if !canConfigure {
			return
		}
		label := "Enable relay"
		if st.enablingRelay {
			label = "Enabling…"
		} else if st.relayErr != "" {
			label = "Retry"
		}
		if ui.PrimaryButton(c, label).Disabled(st.enablingRelay).Clicked() {
			a.enableHomePairingRelay()
		}
	})
	ui.Text(c, "Without relay, connect directly over TCP, Tailscale, or another VPN. No QR code is created.").TextColor(theme.TextMuted)
	ui.Link(c, "Set up Tailscale", pairingTailscaleDocsURL)
}

func (a *App) homePairingOfferBody(c *ui.Context) {
	st := &a.desk.homePairing
	theme := c.Theme()
	ui.Text(c, "Scan this QR code with Paseo on your phone, or copy the link below.").TextColor(theme.TextMuted)
	ui.Column(c).AlignSelf(ui.Center).Padding(12).Radius(12).Border(1, theme.Border).Background(ui.RGB(255, 255, 255)).Children(func() {
		switch {
		case st.offer.QR != nil:
			// The window can be shorter than the phone sheet this mirrors, so
			// the code gives way to the link and warning rather than clipping.
			_, height := c.Size()
			w, _ := ui.ImageSize(st.offer.QR)
			side := min(float32(pairingQRDisplayDIPs), w, max(120, height-320))
			ui.Image(c, st.offer.QR).Size(side, side).Label("Pairing QR code")
		case st.offer.QRError != "":
			ui.Text(c, "QR code unavailable.").TextColor(theme.TextMuted)
		default:
			ui.Text(c, "Rendering QR code…").TextColor(theme.TextMuted)
		}
	})
	ui.Row(c).Gap(8).Children(func() {
		ui.Column(c).Grow(1).Shrink(1).Padding(8).Radius(8).Border(1, theme.Border).Background(theme.Surface).Children(func() {
			ui.Text(c, st.offer.URL).Selectable().TextColor(theme.TextMuted)
		})
		label := "Copy"
		if st.copied {
			label = "Copied"
		}
		if ui.Button(c, label).Clicked() {
			a.copyHomePairingLink()
		}
	})
	ui.Text(c, "Treat this pairing link like a password. Anyone with it can access this daemon.").TextColor(theme.Warning)
	a.homePairingButtons(c, true)
}

func (a *App) homePairingButtons(c *ui.Context, refresh bool) {
	st := &a.desk.homePairing
	ui.Row(c).Gap(8).Children(func() {
		if refresh {
			if ui.Button(c, "Refresh").Disabled(st.busy).Clicked() {
				a.loadHomePairing()
			}
		}
		if ui.Button(c, "Close").Clicked() {
			a.closeHomePairing()
		}
	})
}
