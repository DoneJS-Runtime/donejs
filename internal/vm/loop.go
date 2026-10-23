package vm

import (
	"sort"
	"time"

	"donejs/internal/jit"
)

type timerEntry struct {
	id       uint64
	when     time.Time
	interval time.Duration // 0 => one-shot
	fn       jit.Value
	this     jit.Value
	args     []jit.Value
}

func (rt *Runtime) addTimer(delay time.Duration, interval bool, fn jit.Value, this jit.Value, args []jit.Value) uint64 {
	if delay < 0 {
		delay = 0
	}
	rt.mu.Lock()
	rt.nextID++
	id := rt.nextID
	e := &timerEntry{id: id, when: time.Now().Add(delay), fn: fn, this: this, args: args}
	if interval {
		e.interval = delay
		if e.interval <= 0 {
			e.interval = time.Millisecond
		}
	}
	rt.timers[id] = e
	rt.mu.Unlock()
	return id
}

func (rt *Runtime) clearTimer(id uint64) {
	rt.mu.Lock()
	delete(rt.timers, id)
	rt.mu.Unlock()
}

func (rt *Runtime) nextDeadline() (time.Time, bool) {
	var (
		best time.Time
		ok   bool
	)
	for _, t := range rt.timers {
		if !ok || t.when.Before(best) {
			best = t.when
			ok = true
		}
	}
	return best, ok
}

func (rt *Runtime) fireDueTimers() {
	for {
		now := time.Now()
		var due []*timerEntry
		rt.mu.Lock()
		for _, t := range rt.timers {
			if !t.when.After(now) {
				due = append(due, t)
			}
		}
		sort.Slice(due, func(i, j int) bool {
			if due[i].when.Equal(due[j].when) {
				return due[i].id < due[j].id
			}
			return due[i].when.Before(due[j].when)
		})
		for _, t := range due {
			if t.interval > 0 {
				t.when = now.Add(t.interval)
			} else {
				delete(rt.timers, t.id)
			}
		}
		rt.mu.Unlock()
		if len(due) == 0 {
			return
		}
		for _, t := range due {
			if rt.exitingNow() {
				return
			}
			if _, err := rt.jitCall(t.fn, t.this, t.args...); err != nil {
				rt.reportUncaught(err)
			}
		}
	}
}

func (rt *Runtime) exitingNow() bool {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.exiting
}

// reportUncaught prints an uncaught exception and exits like Node would.
func (rt *Runtime) reportUncaught(err error) {
	panic(uncaught{msg: rt.formatErr(err)})
}

type uncaught struct{ msg string }

func (u uncaught) Error() string { return u.msg }

// Loop drives timers and queued jobs until the runtime has no work left.
func (rt *Runtime) Loop() (code int) {
	defer func() {
		if r := recover(); r != nil {
			if u, ok := r.(uncaught); ok {
				printlnStderr(u.msg)
				code = 1
				return
			}
			panic(r)
		}
	}()
	for {
		if rt.exitingNow() {
			return rt.currentExit()
		}
		rt.drainTicks()
		rt.drainMicrotasks()
		rt.fireDueTimers()
		if rt.exitingNow() {
			return rt.currentExit()
		}
		rt.mu.Lock()
		deadline, hasTimer := rt.nextDeadline()
		pending := len(rt.jobs)
		handles := rt.handles
		inflight := rt.pending
		rt.mu.Unlock()

		if pending == 0 && handles == 0 && inflight == 0 && !hasTimer && !rt.jit.HasJobs() {
			return rt.currentExit()
		}
		if hasTimer {
			d := time.Until(deadline)
			if d < 0 {
				d = 0
			}
			t := time.NewTimer(d)
			select {
			case fn := <-rt.jobs:
				t.Stop()
				fn()
			case <-t.C:
			}
		} else if rt.jit.HasJobs() {
			// Engine jobs (promise reactions, awaited continuations) wake
			// the loop through this branch, so it never blocks while one is
			// pending.
			continue
		} else {
			fn := <-rt.jobs
			fn()
		}
	}
}

func (rt *Runtime) currentExit() int {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.exitCode != 0 {
		return rt.exitCode
	}
	// process.exitCode may have been set from JS.
	if v := rt.processObj.Get("exitCode"); !v.IsUndef() && !v.IsNull() {
		if n := int(v.ToInteger()); n != 0 {
			return n
		}
	}
	return rt.exitCode
}
