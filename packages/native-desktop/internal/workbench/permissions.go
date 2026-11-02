package workbench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/getpaseo/paseo/packages/native-desktop/internal/client"
)

type permissionAction struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Behavior string `json:"behavior"`
}
type permissionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview"`
}
type permissionQuestion struct {
	Question     string             `json:"question"`
	Header       string             `json:"header"`
	Options      []permissionOption `json:"options"`
	MultiSelect  bool               `json:"multiSelect"`
	AllowOther   bool               `json:"allowOther"`
	IsOther      bool               `json:"isOther"`
	AllowEmpty   bool               `json:"allowEmpty"`
	Placeholder  string             `json:"placeholder"`
	DismissLabel string             `json:"dismissLabel"`
}
type permissionRequest struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Kind        string             `json:"kind"`
	Title       string             `json:"title"`
	Description string             `json:"description"`
	Input       map[string]any     `json:"input"`
	Detail      json.RawMessage    `json:"detail"`
	Actions     []permissionAction `json:"actions"`
	Metadata    map[string]any     `json:"metadata"`
}
type permissionResponse struct {
	Behavior         string         `json:"behavior"`
	SelectedActionID string         `json:"selectedActionId,omitempty"`
	UpdatedInput     map[string]any `json:"updatedInput,omitempty"`
	Message          string         `json:"message,omitempty"`
}
type questionAnswer struct {
	selected map[int]bool
	other    string
}
type permissionCard struct {
	request       permissionRequest
	questions     []permissionQuestion
	answers       []questionAnswer
	questionError string
	busy          bool
	unknown       bool
	err           string
}
type permissions struct{ cards []*permissionCard }

func newPermissionCard(r permissionRequest) *permissionCard {
	card := &permissionCard{request: r}
	if r.Kind == "question" {
		raw, err := json.Marshal(r.Input["questions"])
		if err != nil || json.Unmarshal(raw, &card.questions) != nil || len(card.questions) == 0 {
			card.questionError = "Unable to display this question request."
		} else {
			for _, q := range card.questions {
				if q.Header == "" || q.Question == "" || q.Options == nil {
					card.questionError = "Unable to display this question request."
					break
				}
			}
			card.answers = make([]questionAnswer, len(card.questions))
		}
	}
	return card
}
func (p *permissions) find(id string) *permissionCard {
	for _, card := range p.cards {
		if card.request.ID == id {
			return card
		}
	}
	return nil
}
func (p *permissions) remove(id string) {
	for i, card := range p.cards {
		if card.request.ID == id {
			p.cards = append(p.cards[:i], p.cards[i+1:]...)
			return
		}
	}
}
func (p *permissions) put(r permissionRequest) {
	if r.ID == "" {
		return
	}
	if card := p.find(r.ID); card != nil {
		card.request = r
		return
	}
	p.cards = append(p.cards, newPermissionCard(r))
}

// replace applies the agent snapshot in a fetch_agent_timeline_response payload.
func (p *permissions) replace(agent string, raw json.RawMessage) error {
	var v struct {
		AgentID string `json:"agentId"`
		Agent   *struct {
			Pending []permissionRequest `json:"pendingPermissions"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("permission snapshot: %w", err)
	}
	if v.AgentID != agent {
		return errors.New("permission snapshot agent mismatch")
	}
	old := p.cards
	p.cards = nil
	if v.Agent != nil {
		for _, r := range v.Agent.Pending {
			if r.ID == "" {
				continue
			}
			var found *permissionCard
			for _, c := range old {
				if c.request.ID == r.ID {
					found = c
					break
				}
			}
			if found != nil {
				found.request = r
				p.cards = append(p.cards, found)
			} else {
				p.put(r)
			}
		}
	}
	return nil
}

// apply consumes an observed agent_stream permission event or a direct permission event.
func (p *permissions) apply(agent string, raw json.RawMessage) error {
	var v struct {
		Type    string `json:"type"`
		Payload struct {
			AgentID   string            `json:"agentId"`
			Request   permissionRequest `json:"request"`
			RequestID string            `json:"requestId"`
			Event     struct {
				Type      string            `json:"type"`
				Request   permissionRequest `json:"request"`
				RequestID string            `json:"requestId"`
			} `json:"event"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("permission event: %w", err)
	}
	if v.Payload.AgentID != agent {
		return nil
	}
	switch v.Type {
	case "agent_stream":
		switch v.Payload.Event.Type {
		case "permission_requested":
			p.put(v.Payload.Event.Request)
		case "permission_resolved":
			p.remove(v.Payload.Event.RequestID)
		}
	case "agent_permission_request":
		p.put(v.Payload.Request)
	case "agent_permission_resolved":
		p.remove(v.Payload.RequestID)
	}
	return nil
}
func (p *permissions) begin(id string) bool {
	c := p.find(id)
	if c == nil || c.busy || c.unknown {
		return false
	}
	c.busy = true
	c.err = ""
	return true
}
func (p *permissions) finish(id string, err error) {
	c := p.find(id)
	if c == nil {
		return
	}
	c.busy = false
	if err != nil {
		c.err = err.Error()
		c.unknown = errors.Is(err, client.ErrOutcomeUnknown)
		return
	}
	p.remove(id)
}

type permissionCaller interface {
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

func sendPermission(ctx context.Context, c permissionCaller, agent, id string, response permissionResponse) error {
	raw, err := json.Marshal(struct {
		AgentID   string             `json:"agentId"`
		RequestID string             `json:"requestId"`
		Response  permissionResponse `json:"response"`
	}{agent, id, response})
	if err != nil {
		return err
	}
	result, err := c.Call(ctx, "agent_permission_response", raw)
	if err != nil {
		return err
	}
	var ack struct {
		AgentID    string             `json:"agentId"`
		RequestID  string             `json:"requestId"`
		Resolution permissionResponse `json:"resolution"`
	}
	if err := json.Unmarshal(result, &ack); err != nil {
		return fmt.Errorf("invalid permission acknowledgement: %w", err)
	}
	if ack.AgentID != agent || ack.RequestID != id || ack.Resolution.Behavior != response.Behavior {
		return errors.New("invalid permission acknowledgement")
	}
	return nil
}

// submit must be called on the UI thread; post schedules its completion on that thread.
func (p *permissions) submit(ctx context.Context, c permissionCaller, agent, id string, response permissionResponse, post func(func())) {
	if !p.begin(id) {
		return
	}
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		err := sendPermission(callCtx, c, agent, id, response)
		post(func() { p.finish(id, err) })
	}()
}
func questionAnswers(questions []permissionQuestion, values []questionAnswer, dismiss bool) (map[string]string, error) {
	if len(questions) == 0 || len(values) != len(questions) {
		return nil, errors.New("Invalid questions")
	}
	answers := make(map[string]string, len(questions))
	for i, q := range questions {
		var labels []string
		for index, opt := range q.Options {
			if values[i].selected[index] {
				labels = append(labels, opt.Label)
			}
		}
		other := strings.TrimSpace(values[i].other)
		showsText := len(q.Options) == 0 || q.AllowOther || q.IsOther
		if showsText && other != "" {
			if q.MultiSelect {
				labels = append(labels, other)
			} else {
				labels = []string{other}
			}
		}
		if len(labels) > 0 {
			answers[q.Header] = strings.Join(labels, ", ")
			continue
		}
		if showsText && q.AllowEmpty {
			if len(q.Options) == 0 {
				answers[q.Header] = ""
			}
			continue
		}
		if !dismiss {
			return nil, fmt.Errorf("Answer %s before submitting", q.Header)
		}
	}
	return answers, nil
}
func (card *permissionCard) questionResponse(dismiss bool) (permissionResponse, error) {
	if card.questionError != "" {
		return permissionResponse{}, errors.New(card.questionError)
	}
	emptyDismiss := dismiss
	for _, q := range card.questions {
		if !q.AllowEmpty || len(q.Options) != 0 {
			emptyDismiss = false
		}
	}
	if dismiss && !emptyDismiss {
		return permissionResponse{Behavior: "deny", Message: "Dismissed by user"}, nil
	}
	answers, err := questionAnswers(card.questions, card.answers, emptyDismiss)
	if err != nil {
		return permissionResponse{}, err
	}
	input := make(map[string]any, len(card.request.Input)+1)
	for k, v := range card.request.Input {
		input[k] = v
	}
	input["answers"] = answers
	return permissionResponse{Behavior: "allow", UpdatedInput: input}, nil
}

// view renders pending requests and calls respond on the UI thread.
func (p *permissions) view(c *ui.Context, respond func(string, permissionResponse)) {
	for _, card := range p.cards {
		card := card
		c := c.Key(card.request.ID)
		disabled := card.busy || card.unknown
		ui.Column(c).Gap(4).Children(func() {
			title := card.request.Title
			if title == "" {
				title = card.request.Name
			}
			if title == "" {
				title = "Permission required"
			}
			ui.Text(c, title).Bold()
			if card.request.Description != "" {
				ui.Text(c, card.request.Description)
			}
			if card.request.Kind == "question" {
				if card.questionError != "" {
					ui.Text(c, card.questionError)
				} else {
					for i, q := range card.questions {
						i, q := i, q
						ui.Text(c, q.Question).Bold()
						for j, opt := range q.Options {
							j, opt := j, opt
							selected := card.answers[i].selected[j]
							label := "○ " + opt.Label
							if q.MultiSelect {
								label = "☐ " + opt.Label
							}
							if selected {
								if q.MultiSelect {
									label = "☑ " + opt.Label
								} else {
									label = "● " + opt.Label
								}
							}
							if ui.Button(c, label).Disabled(disabled).Clicked() {
								if card.answers[i].selected == nil || !q.MultiSelect {
									card.answers[i].selected = make(map[int]bool)
								}
								card.answers[i].selected[j] = !selected
								if !q.MultiSelect {
									card.answers[i].other = ""
								}
							}
							if opt.Description != "" {
								ui.Text(c, opt.Description)
							}
							if opt.Preview != "" {
								ui.Text(c, opt.Preview)
							}
						}
						if len(q.Options) == 0 || q.AllowOther || q.IsOther {
							before := card.answers[i].other
							placeholder := q.Placeholder
							if placeholder == "" {
								placeholder = "Your answer"
							}
							ui.TextInput(c, &card.answers[i].other).Placeholder(placeholder).Label(placeholder).Disabled(disabled)
							if !q.MultiSelect && card.answers[i].other != before && card.answers[i].other != "" {
								card.answers[i].selected = nil
							}
						}
					}
					ui.Row(c).Gap(6).Children(func() {
						if ui.PrimaryButton(c, "Submit").Disabled(disabled).Clicked() {
							r, err := card.questionResponse(false)
							if err != nil {
								card.err = err.Error()
							} else {
								respond(card.request.ID, r)
							}
						}
						label := "Dismiss"
						for _, q := range card.questions {
							if q.DismissLabel != "" {
								label = q.DismissLabel
								break
							}
						}
						if ui.Button(c, label).Disabled(disabled).Clicked() {
							r, err := card.questionResponse(true)
							if err != nil {
								card.err = err.Error()
							} else {
								respond(card.request.ID, r)
							}
						}
					})
				}
			} else {
				if plan, ok := card.request.Metadata["planText"].(string); ok && plan != "" {
					ui.Text(c, plan)
				} else if plan, ok := card.request.Input["plan"].(string); ok && plan != "" {
					ui.Text(c, plan)
				} else if len(card.request.Detail) > 0 && string(card.request.Detail) != "null" {
					var detail any
					if json.Unmarshal(card.request.Detail, &detail) == nil {
						if raw, err := json.MarshalIndent(detail, "", "  "); err == nil {
							ui.Text(c, string(raw))
						}
					}
				} else if len(card.request.Input) > 0 {
					if raw, err := json.MarshalIndent(card.request.Input, "", "  "); err == nil {
						ui.Text(c, string(raw))
					}
				}
				actions := card.request.Actions
				if len(actions) == 0 {
					label := "Approve"
					if card.request.Kind == "plan" {
						label = "Implement"
					}
					actions = []permissionAction{{ID: "reject", Label: "Deny", Behavior: "deny"}, {ID: "accept", Label: label, Behavior: "allow"}}
				}
				ui.Row(c).Gap(6).Children(func() {
					for _, action := range actions {
						action := action
						if action.Behavior != "allow" && action.Behavior != "deny" {
							continue
						}
						if ui.Button(c, action.Label).Disabled(disabled).Clicked() {
							r := permissionResponse{Behavior: action.Behavior, SelectedActionID: action.ID}
							if r.Behavior == "deny" {
								r.Message = "Denied by user"
							}
							respond(card.request.ID, r)
						}
					}
				})
			}
			if card.busy {
				ui.Text(c, "Submitting permission response…")
			}
			if card.err != "" {
				ui.Text(c, "Permission response failed: "+card.err)
			}
		})
	}
}
