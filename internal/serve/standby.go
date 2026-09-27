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
//
// Privacy guarantees:
//   - Any line observed while paused (under either hard capture mute or software standby)
//     is permanently suppressed and will never be broadcast in Transcript.Lines or FinalizedLines,
//     neither during pause, on the resume update, nor in any subsequent update.
//   - If an active wake phrase matches a newly finalized line in standby mode, that specific
//     wake-phrase line is un-suppressed, the session is automatically resumed, and a DisplayCard
//     notification is published. All ambient lines that finalized before or alongside the wake
//     phrase remain strictly suppressed.
//   - Terminal session events (u.Done) and errors (u.Err) are always forwarded to subscribers,
//     even if the session was paused when termination occurred.
func filterStandbyUpdates(ctx context.Context, in <-chan session.Update, sessCtrl *LiveSessionControl, hub *Hub) <-chan session.Update {
	out := make(chan session.Update)

	go func() {
		defer close(out)

		completedPriorToPause := make(map[uint64]struct{})
		suppressed := make(map[uint64]struct{})

		for {
			select {
			case <-ctx.Done():
				return
			case u, ok := <-in:
				if !ok {
					return
				}

				if !sessCtrl.IsPaused() {
					for _, l := range u.Transcript.Lines {
						if l.IsComplete {
							if _, bad := suppressed[l.ID]; !bad {
								completedPriorToPause[l.ID] = struct{}{}
							}
						}
					}
					select {
					case out <- redactUpdate(u, suppressed):
					case <-ctx.Done():
						return
					}
					continue
				}

				// Session is paused.
				// Record all lines seen while paused that were not completed prior to pause.
				for _, l := range u.Transcript.Lines {
					if _, prior := completedPriorToPause[l.ID]; !prior {
						suppressed[l.ID] = struct{}{}
					}
				}

				// Hard privacy mute: drop audio/transcript updates, but forward terminal Done or Err.
				if sessCtrl.MuteCapture() {
					if u.Done || u.Err != nil {
						select {
						case out <- redactUpdate(u, suppressed):
						case <-ctx.Done():
							return
						}
					}
					continue
				}

				// Standby mode: wake phrases may be active.
				phrases := sessCtrl.PassthroughPhrases()
				if len(phrases) == 0 {
					if u.Done || u.Err != nil {
						select {
						case out <- redactUpdate(u, suppressed):
						case <-ctx.Done():
							return
						}
					}
					continue
				}

				// Check newly finalized lines for a wake phrase match.
				matchedPhrase := ""
				var matchedLineID uint64
				if len(u.FinalizedLines) > 0 {
					finalizedMap := make(map[uint64]moonshine.Line, len(u.Transcript.Lines))
					for _, l := range u.Transcript.Lines {
						if l.IsComplete {
							finalizedMap[l.ID] = l
						}
					}

					for _, lt := range u.FinalizedLines {
						if l, exists := finalizedMap[lt.ID]; exists {
							if p, matched := matchWakePhrase(l.Text, phrases); matched {
								matchedPhrase = p
								matchedLineID = l.ID
								break
							}
						}
					}
				}

				if matchedPhrase != "" {
					// Wake phrase matched! Un-suppress the wake phrase line and record it as completed.
					delete(suppressed, matchedLineID)
					completedPriorToPause[matchedLineID] = struct{}{}

					_ = sessCtrl.Resume(ctx)
					if hub != nil {
						hub.Publish(event.DisplayCard{
							Title: "Listening Resumed",
							Body:  fmt.Sprintf("Voice session resumed by wake phrase: %q", matchedPhrase),
							Kind:  "session",
						})
					}
					select {
					case out <- redactUpdate(u, suppressed):
					case <-ctx.Done():
						return
					}
					continue
				}

				// No wake phrase matched.
				// Drop normal interim and ambient finalized updates during standby,
				// but forward terminal Done or Err updates.
				if u.Done || u.Err != nil {
					select {
					case out <- redactUpdate(u, suppressed):
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	return out
}

func redactUpdate(u session.Update, suppressed map[uint64]struct{}) session.Update {
	if len(suppressed) == 0 {
		return u
	}

	linesChanged := false
	var redactedLines []moonshine.Line
	for _, l := range u.Transcript.Lines {
		if _, bad := suppressed[l.ID]; bad {
			linesChanged = true
			continue
		}
		redactedLines = append(redactedLines, l)
	}

	finalizedChanged := false
	var redactedFinalized []session.LineTiming
	for _, lt := range u.FinalizedLines {
		if _, bad := suppressed[lt.ID]; bad {
			finalizedChanged = true
			continue
		}
		redactedFinalized = append(redactedFinalized, lt)
	}

	if !linesChanged && !finalizedChanged {
		return u
	}

	out := u
	if linesChanged {
		out.Transcript.Lines = redactedLines
	}
	if finalizedChanged {
		out.FinalizedLines = redactedFinalized
	}
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
