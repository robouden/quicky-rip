package classify

import (
	"context"
	"testing"

	"codeberg.org/Safecast/quicky/internal/llm"
)

type stub struct {
	reply  string
	called bool
}

func (s *stub) Text(context.Context, llm.Request) (string, error) {
	s.called = true
	return s.reply, nil
}
func (s *stub) Tool(context.Context, llm.Request, llm.Tool, any) error {
	panic("unused")
}

func TestDecisionOrder(t *testing.T) {
	cases := []struct {
		name       string
		in         Input
		reply      string
		wantClass  Class
		wantConfim bool
		wantLLM    bool
	}{
		{
			name:       "subject confirm beats list headers",
			in:         Input{Subject: "Confirm your subscription", ListID: "<list.example.com>"},
			wantClass:  Transactional,
			wantConfim: true,
		},
		{
			name:      "list headers resolve without a model call",
			in:        Input{Subject: "Issue #42", ListUnsub: "<mailto:u@example.com>"},
			wantClass: Newsletter,
		},
		{
			name:      "precedence bulk resolves without a model call",
			in:        Input{Subject: "Issue #42", Precedence: "Bulk"},
			wantClass: Newsletter,
		},
		{
			name:      "ambiguous falls through to the model",
			in:        Input{Subject: "Hey", FromEmail: "a@b.com"},
			reply:     "newsletter",
			wantClass: Newsletter,
			wantLLM:   true,
		},
		{
			name:       "non-newsletter still surfaces a confirmation link in the body",
			in:         Input{Subject: "Hey", HTML: `<a href="https://x.test/verify?t=1">click</a>`},
			reply:      "not_newsletter",
			wantClass:  Transactional,
			wantConfim: true,
			wantLLM:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &stub{reply: tc.reply}
			got, err := Classify(context.Background(), s, "m", tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Class != tc.wantClass || got.NeedsConfirmation != tc.wantConfim {
				t.Fatalf("got %+v, want class=%s confirm=%v", got, tc.wantClass, tc.wantConfim)
			}
			if s.called != tc.wantLLM {
				t.Fatalf("llm called = %v, want %v", s.called, tc.wantLLM)
			}
		})
	}
}
