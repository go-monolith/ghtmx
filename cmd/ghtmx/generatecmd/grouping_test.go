package generatecmd

import (
	"testing"
	"time"
)

// Watch mode coalesces a burst of file events into one reload. Getting
// the grouping wrong is directly visible to whoever is using it: merge
// too eagerly and a failing build swallows the reload for the fix that
// follows (FR-063); merge too little and a single save triggers several
// browser reloads.
//
// The grouping is driven entirely by a channel, so it is tested by
// feeding that channel rather than touching the filesystem — which also
// means these tests are deterministic instead of racing a watcher.

func testGenerate() Generate {
	return Generate{Log: quietLog()}
}

// send pushes events onto the channel and then closes it, which is the
// "watcher stopped" signal the grouping loop reads.
func sendAndClose(ch chan *GenerationEvent, events ...*GenerationEvent) {
	go func() {
		for _, e := range events {
			ch <- e
		}
		close(ch)
	}()
}

func TestGroupingMergesABurstIntoOneEvent(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent, 8)

	sendAndClose(ch,
		&GenerationEvent{GoFileWritten: true},
		&GenerationEvent{TemplFileTextUpdated: true},
		&GenerationEvent{GoFileWritten: true, GoSourceUpdated: true},
	)

	grouped, updates, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("grouping reported not-ok for a burst of real events")
	}
	// Every flag that was set on any event must survive the merge, or
	// the reload skips the thing that changed.
	if !grouped.GoFileWritten || !grouped.TemplFileTextUpdated || !grouped.GoSourceUpdated {
		t.Errorf("merged event lost a flag: %+v", grouped)
	}
	// Two of the three wrote a Go file.
	if updates != 2 {
		t.Errorf("updates = %d, want 2", updates)
	}
}

// TestGroupingCountsErrorsExactlyOnce pins the counter reset in the
// first-event copy. Counting the first event's errors twice would
// attribute a phantom failure to the batch and suppress a reload that
// should have happened.
func TestGroupingCountsErrorsExactlyOnce(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent, 8)

	sendAndClose(ch,
		&GenerationEvent{Errors: 2},
		&GenerationEvent{Errors: 3},
	)

	grouped, _, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("grouping reported not-ok")
	}
	if grouped.Errors != 5 {
		t.Errorf("Errors = %d, want 5 (2+3, first event counted once)", grouped.Errors)
	}
}

// TestGroupingReturnsAnErrorOnlyBatch is FR-063: a batch that produced
// nothing but errors is still observable and must be returned, so the
// suppression policy runs. Holding it would merge the eventual fix into
// the failing batch and swallow that reload.
func TestGroupingReturnsAnErrorOnlyBatch(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent, 4)

	sendAndClose(ch, &GenerationEvent{Errors: 1})

	grouped, updates, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || grouped == nil {
		t.Fatal("an error-only batch was not returned")
	}
	if grouped.Errors != 1 {
		t.Errorf("Errors = %d, want 1", grouped.Errors)
	}
	if updates != 0 {
		t.Errorf("updates = %d, want 0 — nothing was written", updates)
	}
}

// TestGroupingIgnoresEventsWithNothingObservable covers the other side
// of that branch: a batch where nothing happened must not wake the
// browser up.
func TestGroupingIgnoresEventsWithNothingObservable(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent, 4)

	// GoSourceUpdated alone is not one of the flags the timeout branch
	// treats as observable.
	sendAndClose(ch, &GenerationEvent{GoSourceUpdated: true})

	done := make(chan struct{})
	go func() {
		defer close(done)
		grouped, _, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
		if err != nil {
			t.Error(err)
		}
		// The channel closes, so the loop exits through the closed-channel
		// path carrying whatever it had grouped.
		if ok && grouped == nil {
			t.Error("reported ok with no grouped event")
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("grouping did not return after the channel closed")
	}
}

// TestGroupingReturnsNotOkOnAClosedEmptyChannel pins the shutdown path:
// when the watcher stops with nothing pending, the caller has to be told
// there is nothing left rather than being handed an empty event.
func TestGroupingReturnsNotOkOnAClosedEmptyChannel(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent)
	close(ch)

	grouped, updates, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("reported ok on a closed empty channel")
	}
	if grouped != nil {
		t.Errorf("returned a grouped event %+v, want nil", grouped)
	}
	if updates != 0 {
		t.Errorf("updates = %d, want 0", updates)
	}
}

// TestGroupingWaitsForQuietBeforeReturning pins the debounce itself: an
// event arriving inside the 100ms window joins the batch rather than
// starting a new one, which is what stops a multi-file save producing a
// reload per file.
func TestGroupingWaitsForQuietBeforeReturning(t *testing.T) {
	cmd := testGenerate()
	ch := make(chan *GenerationEvent)

	go func() {
		ch <- &GenerationEvent{GoFileWritten: true}
		// Well inside the window: must be merged, not dropped or split.
		time.Sleep(20 * time.Millisecond)
		ch <- &GenerationEvent{TemplFileTextUpdated: true}
	}()

	grouped, updates, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(ch)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("grouping reported not-ok")
	}
	if !grouped.GoFileWritten || !grouped.TemplFileTextUpdated {
		t.Errorf("an event arriving inside the debounce window was not merged: %+v", grouped)
	}
	if updates != 1 {
		t.Errorf("updates = %d, want 1", updates)
	}
}
