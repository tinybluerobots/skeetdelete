package progress

import (
	"sync"
	"syscall/js"

	"github.com/jon-cooper/skeetdelete/internal/types"
)

type Tracker struct {
	mu      sync.Mutex
	current types.Progress
	cb      js.Func
	cbSet   bool
}

func NewTracker() *Tracker {
	return &Tracker{
		current: types.Progress{
			State: "idle",
		},
	}
}

func (t *Tracker) Update(p types.Progress) {
	t.mu.Lock()
	t.current = p
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) Get() types.Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.current
}

func (t *Tracker) SetState(state string) {
	t.mu.Lock()
	t.current.State = state
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) IncrementFound() {
	t.mu.Lock()
	t.current.RecordsFound++
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) IncrementDeleted() {
	t.mu.Lock()
	t.current.RecordsDeleted++
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) IncrementSkipped() {
	t.mu.Lock()
	t.current.RecordsSkipped++
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) SetCurrentAction(action string) {
	t.mu.Lock()
	t.current.CurrentAction = action
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) SetError(err string) {
	t.mu.Lock()
	t.current.State = "error"
	t.current.ErrorMessage = err
	t.mu.Unlock()
	t.notify()
}

func (t *Tracker) ToJS() js.Value {
	t.mu.Lock()
	p := t.current
	t.mu.Unlock()

	obj := js.Global().Get("Object").New()
	obj.Set("state", p.State)
	obj.Set("records_found", p.RecordsFound)
	obj.Set("records_deleted", p.RecordsDeleted)
	obj.Set("records_skipped", p.RecordsSkipped)
	obj.Set("est_remaining", p.EstRemaining)
	obj.Set("current_action", p.CurrentAction)
	obj.Set("is_dry_run", p.IsDryRun)
	if p.ErrorMessage != "" {
		obj.Set("error_message", p.ErrorMessage)
	}
	return obj
}

func (t *Tracker) OnUpdate(cb js.Func) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cbSet {
		t.cb.Release()
	}
	t.cb = cb
	t.cbSet = true
}

func (t *Tracker) ReleaseCallback() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cbSet {
		t.cb.Release()
		t.cbSet = false
	}
}

func (t *Tracker) notify() {
	t.mu.Lock()
	cbVal := t.cb.Value
	hasCb := t.cbSet
	t.mu.Unlock()

	if hasCb && cbVal.Truthy() {
		cbVal.Invoke(t.ToJS())
	}
}
