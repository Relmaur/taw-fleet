package agent

import "strings"

// Claude's multiple-choice questions (its AskUserQuestion tool) arrive as a
// permission prompt. The dashboard shows them as a picker and answers with
// the person's choices, which Claude Code hands back to Claude.

// AskTool is Claude Code's tool for asking the person.
const AskTool = "AskUserQuestion"

// Choice is one of Claude's multiple-choice questions.
type Choice struct {
	Text    string
	Header  string // a short label ("build-site")
	Multi   bool   // more than one option can be picked
	Options []Option
}

// Option is one answer Claude offers.
type Option struct {
	Label       string
	Description string
}

// Choices are r's questions, or nil when r isn't Claude asking.
func Choices(r *Request) []Choice {
	if r == nil || r.Tool != AskTool {
		return nil
	}
	list, _ := r.Input["questions"].([]any)
	var out []Choice
	for _, item := range list {
		q, ok := item.(map[string]any)
		if !ok {
			continue
		}
		str := func(m map[string]any, k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		multi, _ := q["multiSelect"].(bool)
		qq := Choice{Text: str(q, "question"), Header: str(q, "header"), Multi: multi}
		opts, _ := q["options"].([]any)
		for _, o := range opts {
			if om, ok := o.(map[string]any); ok && str(om, "label") != "" {
				qq.Options = append(qq.Options, Option{Label: str(om, "label"), Description: str(om, "description")})
			}
		}
		if qq.Text != "" {
			out = append(out, qq)
		}
	}
	return out
}

// AnswerQuestions answers Claude's questions: question → the picked label
// (several joined with ", "), or what the person typed.
func (s *Session) AnswerQuestions(r *Request, answers map[string]string) error {
	in := make(map[string]any, len(r.Input)+1)
	for k, v := range r.Input {
		in[k] = v
	}
	in["answers"] = answers
	return s.send(map[string]any{"type": "control_response", "response": map[string]any{
		"subtype": "success", "request_id": r.ID, "response": map[string]any{"behavior": "allow", "updatedInput": in}}})
}
