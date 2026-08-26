package agentflow_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghchinoy/moonshine-go/pkg/agentflow"
	"github.com/ghchinoy/moonshine-go/pkg/serveapi"
)

func TestAgentFlow_AskConfirmChooseFlow(t *testing.T) {
	var spoken []string
	var spokenMu sync.Mutex

	af := agentflow.New()
	af.SpeakWith(func(text string) error {
		spokenMu.Lock()
		spoken = append(spoken, text)
		spokenMu.Unlock()
		return nil
	})

	var chosenSSID string
	var confirmed bool

	af.ListenFor("set up wifi", func(d *agentflow.Dialog) error {
		ssid, err := d.Ask("What's the name of your wifi network?")
		if err != nil {
			return err
		}
		chosenSSID = ssid

		ok, err := d.Confirm("Is that right?")
		if err != nil {
			return err
		}
		confirmed = ok

		if ok {
			_ = d.Say("Connecting to " + ssid)
		}
		return nil
	})

	// Start flow
	settle1 := af.RegisterSettle()
	af.HandleUtterance("set up wifi")
	_ = settle1.Wait(context.Background())

	if !af.IsActive() {
		t.Fatalf("expected flow to be active waiting for Ask response")
	}

	// Supply answer to Ask
	settle2 := af.RegisterSettle()
	af.HandleUtterance("HomeNetwork")
	_ = settle2.Wait(context.Background())

	if chosenSSID != "HomeNetwork" {
		t.Errorf("chosenSSID = %q; want %q", chosenSSID, "HomeNetwork")
	}

	// Supply answer to Confirm ("yes")
	settle3 := af.RegisterSettle()
	af.HandleUtterance("yes")
	_ = settle3.Wait(context.Background())

	if !confirmed {
		t.Errorf("expected confirmed = true")
	}

	if af.IsActive() {
		t.Errorf("expected flow to be completed and inactive")
	}

	spokenMu.Lock()
	defer spokenMu.Unlock()
	if len(spoken) < 3 {
		t.Errorf("expected at least 3 spoken lines, got %d: %v", len(spoken), spoken)
	}
}

func TestAgentFlow_CancelAndRestart(t *testing.T) {
	af := agentflow.New()
	af.SpeakWith(func(text string) error { return nil })

	restarted := false
	cancelled := false

	af.ListenFor("book appointment", func(d *agentflow.Dialog) error {
		ans, err := d.Ask("What day?")
		if err != nil {
			if _, ok := err.(agentflow.DialogCancelled); ok {
				cancelled = true
			}
			return err
		}
		if ans == "restart" {
			restarted = true
			return d.Restart()
		}
		return nil
	})

	// Trigger flow
	s1 := af.RegisterSettle()
	af.HandleUtterance("book appointment")
	_ = s1.Wait(context.Background())

	// Send restart
	s2 := af.RegisterSettle()
	af.HandleUtterance("restart")
	_ = s2.Wait(context.Background())

	if !restarted {
		t.Errorf("expected flow restart to be triggered")
	}

	// Cancel active flow via cancel global
	s3 := af.RegisterSettle()
	af.HandleUtterance("cancel")
	_ = s3.Wait(context.Background())

	if !cancelled {
		t.Errorf("expected flow to be cancelled")
	}
}

func TestAgentFlow_OtherwiseUnmatched(t *testing.T) {
	af := agentflow.New()
	af.SpeakWith(func(text string) error { return nil })

	var unmatched []string
	af.Otherwise(func(utterance string) {
		unmatched = append(unmatched, utterance)
	})

	af.ListenFor("turn on lights", func(d *agentflow.Dialog) error {
		return d.Say("Lights on")
	})

	af.HandleUtterance("hello world")

	if len(unmatched) != 1 || unmatched[0] != "hello world" {
		t.Errorf("expected unmatched ['hello world'], got %v", unmatched)
	}
}

func TestSpellOut(t *testing.T) {
	got := agentflow.SpellOut("wifi")
	if got != "w i f i" {
		t.Errorf("SpellOut(wifi) = %q; want %q", got, "w i f i")
	}
}

func TestHandlerAdapter(t *testing.T) {
	af := agentflow.New()
	var heard []string
	af.OnHeard(func(u string) {
		heard = append(heard, u)
	})

	adapter := agentflow.NewHandlerAdapter(af)
	line := serveapi.Line{ID: 1, Text: "hello agent"}

	actions := adapter.OnFinalizedLine(context.Background(), line)
	if len(actions) != 0 {
		t.Errorf("expected 0 actions from adapter, got %d", len(actions))
	}

	if len(heard) != 1 || heard[0] != "hello agent" {
		t.Errorf("expected heard ['hello agent'], got %v", heard)
	}
}

func TestAgentFlow_Choose(t *testing.T) {
	af := agentflow.New()
	af.SpeakWith(func(text string) error { return nil })

	var selectedChoice string

	af.ListenFor("pick color", func(d *agentflow.Dialog) error {
		choices := map[string][]string{
			"red":  {"crimson", "ruby"},
			"blue": {"azure", "navy"},
		}
		choice, err := d.Choose("What color do you prefer?", choices)
		if err != nil {
			return err
		}
		selectedChoice = choice
		return nil
	})

	s1 := af.RegisterSettle()
	af.HandleUtterance("pick color")
	_ = s1.Wait(context.Background())

	s2 := af.RegisterSettle()
	af.HandleUtterance("azure")
	_ = s2.Wait(context.Background())

	if selectedChoice != "blue" {
		t.Errorf("expected choice 'blue', got %q", selectedChoice)
	}
}

func TestAgentFlow_AskOptionsTimeout(t *testing.T) {
	af := agentflow.New()
	af.SpeakWith(func(text string) error { return nil })

	var askErr error

	af.ListenFor("quick question", func(d *agentflow.Dialog) error {
		_, err := d.Ask("Are you there?", agentflow.AskOptions{
			Timeout:    50 * time.Millisecond,
			MaxRetries: 1,
		})
		askErr = err
		return err
	})

	s1 := af.RegisterSettle()
	af.HandleUtterance("quick question")
	_ = s1.Wait(context.Background())

	// Wait long enough for timeout retries to expire
	time.Sleep(200 * time.Millisecond)

	if askErr == nil {
		t.Errorf("expected timeout error from Ask")
	} else if _, ok := askErr.(agentflow.DialogNoMatch); !ok {
		t.Errorf("expected DialogNoMatch error, got %v", askErr)
	}
}

func TestAgentFlow_LanguageAndVoiceBuilders(t *testing.T) {
	af := agentflow.New()
	af.Language("es").
		Voice("kokoro_ef_dora").
		ModelArch("tiny-streaming").
		Microphone(true).
		Speech(true).
		TriggerThreshold(0.8)

	if strings.Contains(af.ActiveTrigger(), "nonexistent") {
		t.Errorf("unexpected trigger")
	}
}

func TestAgentFlow_ActionSink(t *testing.T) {
	var dispatched []serveapi.ActionRequest
	var mu sync.Mutex

	sink := serveapi.ActionSinkFunc(func(ctx context.Context, req serveapi.ActionRequest) (serveapi.ActionResult, error) {
		mu.Lock()
		dispatched = append(dispatched, req)
		mu.Unlock()
		return serveapi.ActionResult{ID: req.ID, OK: true}, nil
	})

	af := agentflow.New().ActionSink(sink)
	af.SpeakWith(func(text string) error { return nil })

	af.ListenFor("pause session", func(d *agentflow.Dialog) error {
		_, err := d.PauseListening()
		return err
	})

	s1 := af.RegisterSettle()
	af.HandleUtterance("pause session")
	_ = s1.Wait(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(dispatched) != 1 || dispatched[0].Verb != "session.pause" {
		t.Errorf("expected 1 session.pause action, got %v", dispatched)
	}
}

func TestAgentFlow_SpeakStreamWith(t *testing.T) {
	var tokens []string
	var mu sync.Mutex

	af := agentflow.New()
	af.SpeakStreamWith(func(textCh <-chan string) error {
		for tok := range textCh {
			mu.Lock()
			tokens = append(tokens, tok)
			mu.Unlock()
		}
		return nil
	})

	var saidText string
	af.OnSaid(func(text string) {
		saidText = text
	})

	ch := make(chan string, 3)
	ch <- "Hello "
	ch <- "streaming "
	ch <- "world."
	close(ch)

	if err := af.SayStream(ch); err != nil {
		t.Fatalf("SayStream: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(tokens) != 3 {
		t.Errorf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
	if saidText != "Hello streaming world." {
		saidTextWant := "Hello streaming world."
		t.Errorf("saidText = %q, want %q", saidText, saidTextWant)
	}
}

func TestAgentFlow_Dialog_SayStream_SentenceChunking(t *testing.T) {
	var sentences []string
	var mu sync.Mutex

	af := agentflow.New()
	af.SpeakWith(func(text string) error {
		mu.Lock()
		sentences = append(sentences, text)
		mu.Unlock()
		return nil
	})

	af.ListenFor("read news", func(d *agentflow.Dialog) error {
		ch := make(chan string, 6)
		ch <- "Here is "
		ch <- "the first story. "
		ch <- "And here is "
		ch <- "the second one."
		close(ch)
		return d.SayStream(ch)
	})

	s1 := af.RegisterSettle()
	af.HandleUtterance("read news")
	_ = s1.Wait(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if len(sentences) < 2 {
		t.Errorf("expected at least 2 sentences chunked, got %d: %v", len(sentences), sentences)
	}
}
