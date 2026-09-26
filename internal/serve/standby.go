package serve

import (
	"context"
	"fmt"
	"strings"

	"github.com/ghchinoy/moonshine-go/internal/serve/event"
	"github.com/ghchinoy/moonshine-go/internal/session"
	"github.com/ghchinoy/moonshine-go/pkg/moonshine"
)

// filterStandbyUpdates filters incoming session.Update events during paused/standby state.
// When unpaused, all updates pass through unchanged.
// When paused with no wake phrases (hard privacy mute), all updates are dropped.
// When paused in standby mode (wake phrases active):
//   - Interim updates are dropped so ambient conversation is never broadcast.
//   - Finalized lines are matched against active wake phrases.
//   - If a wake phrase matches, the session resumes via sessCtrl.Resume(),
//     a DisplayCard notification is published, and the update is forwarded.
//   - Non-matching finalized lines are dropped.
func filterStandbyUpdates(ctx context.Context, in <-chan session.Update, sessCtrl *LiveSessionControl, hub *Hub) <-chan session.Update {
	out := make(chan session.Update)

	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-in:
				if !ok {
					return
				}

				if !sessCtrl.IsPaused() {
					select {
					case out <- u:
					case <-ctx.Done():
						return
					}
					continue
				}

				// Session is paused.
				if sessCtrl.MuteCapture() {
					// Hard privacy mute: drop everything.
					continue
				}

				// Standby mode: drop interim updates.
				if !u.Done && len(u.FinalizedLines) == 0 {
					continue
				}

				phrases := sessCtrl.PassthroughPhrases()
				if len(phrases) == 0 {
					continue
				}

				// Check finalized lines for a wake phrase match.
				matchedPhrase := ""
				finalizedMap := make(map[uint64]moonshine.Line)
				for _, l := range u.Transcript.Lines {
					if l.IsComplete {
						finalizedMap[l.ID] = l
					}
				}

				for _, lt := range u.FinalizedLines {
					if l, exists := finalizedMap[lt.ID]; exists {
						if p, matched := matchWakePhrase(l.Text, phrases); matched {
							matchedPhrase = p
							break
						}
					}
				}

				// Also check all lines if FinalizedLines is empty but u.Done is true
				if matchedPhrase == "" && u.Done {
					for _, l := range u.Transcript.Lines {
						if l.IsComplete {
							if p, matched := matchWakePhrase(l.Text, phrases); matched {
								matchedPhrase = p
								break
							}
						}
					}
				}

				if matchedPhrase != "" {
					_ = sessCtrl.Resume(ctx)
					if hub != nil {
						hub.Publish(event.DisplayCard{
							Title: "Listening Resumed",
							Body:  fmt.Sprintf("Voice session resumed by wake phrase: %q", matchedPhrase),
							Kind:  "session",
						})
					}
					select {
					case out <- u:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return out
}

func matchWakePhrase(text string, phrases []string) (string, bool) {
	normText := normalizeForWakeMatching(text)
	if normText == "" {
		return "", false
	}
	paddedText := " " + normText + " "
	for _, p := range phrases {
		normP := normalizeForWakeMatching(p)
		if normP == "" {
			continue
		}
		paddedP := " " + normP + " "
		if strings.Contains(paddedText, paddedP) {
			return p, true
		}
	}
	return "", false
}

func normalizeForWakeMatching(s string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune(' ') // replace punctuation with whitespace
		}
	}
	return strings.Join(strings.Fields(sb.String()), " ")
}
