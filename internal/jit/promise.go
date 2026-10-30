package jit

// promise.go implements the Promise builtin and the microtask queue that
// `await` resumes on. Promises are host values: opaque to the interpreter
// except through Get (then/catch/finally), New (the constructor) and the
// settlement helpers the await opcode uses.

const (
	pending = iota
	fulfilled
	rejected
)

// promiseReaction is one subscriber, run on the microtask queue when the
// promise settles.
type promiseReaction struct {
	fn  func(v Value, rejected bool)
	sub *Promise
}

// Promise is a JS promise.
type Promise struct {
	vm        *VM
	state     int
	value     Value
	reason    Value
	reactions []promiseReaction
	// bm memoizes the then/catch/finally wrappers returned by Get so repeated
	// method calls on the same promise skip the closure allocation.
	bm map[string]Value
}

func newPromise(vm *VM) *Promise {
	return &Promise{vm: vm, value: Undef(), reason: Undef()}
}

// PromiseVal boxes a promise so the interpreter can carry it on the stack.
func PromiseVal(p *Promise) Value { return Host(p) }

// AsPromise unwraps a promise value, or nil.
func (v Value) AsPromise() *Promise {
	if p, ok := v.ref.(*Promise); ok {
		return p
	}
	return nil
}

// isCallable reports whether v can be called as a function.
func isCallable(v Value) bool {
	_, cl := v.FuncParts()
	return cl != nil || v.AsFunc() != nil || v.AsBuiltin() != nil || v.AsHost() != nil
}

// settle records the settlement and schedules the subscribers.
func (p *Promise) settle(state int, v Value) {
	if p.state != pending {
		return
	}
	p.state = state
	if state == fulfilled {
		p.value = v
	} else {
		p.reason = v
	}
	rs := p.reactions
	p.reactions = nil
	for _, r := range rs {
		r := r
		p.vm.enqueue(func() { r.fn(v, state == rejected) })
	}
}

// resolve fulfills the promise, adopting the state of a thenable value.
func (p *Promise) resolve(v Value) {
	if sub := v.AsPromise(); sub != nil {
		if sub == p {
			p.settle(rejected, strErr("TypeError", "Chaining cycle detected for promise"))
			return
		}
		sub.subscribeFn(func(val Value, rej bool) {
			if rej {
				p.reject(val)
			} else {
				p.resolve(val)
			}
		}, nil)
		return
	}
	p.settle(fulfilled, v)
}

func (p *Promise) reject(v Value) { p.settle(rejected, v) }

// subscribe registers a reaction. onFul and onRej are JS values; pass Undef
// for the one that should propagate the settlement instead.
func (p *Promise) subscribe(onFul, onRej Value, sub *Promise) {
	p.subscribeFn(func(v Value, rej bool) {
		fn := onFul
		if rej {
			fn = onRej
		}
		if sub == nil {
			return
		}
		if !isCallable(fn) {
			if rej {
				sub.reject(v)
			} else {
				sub.resolve(v)
			}
			return
		}
		res, err := p.vm.callCallable(fn, []Value{v})
		if err != nil {
			sub.reject(errToValue(err))
			return
		}
		sub.resolve(res)
	}, sub)
}

func (p *Promise) subscribeFn(fn func(Value, bool), sub *Promise) {
	r := promiseReaction{fn: fn, sub: sub}
	if p.state == pending {
		p.reactions = append(p.reactions, r)
		return
	}
	v, rej := p.value, false
	if p.state == rejected {
		v, rej = p.reason, true
	}
	p.vm.enqueue(func() { fn(v, rej) })
}

// ---- HostValue ----

func (p *Promise) Call([]Value) Value {
	return strErr("TypeError", "Promise constructor cannot be called without 'new'")
}

// New runs the executor of `new Promise(executor)`.
func (p *Promise) New([]Value) Value { return Host(p) }

// Get returns a promise method, memoizing the wrapper on the promise itself.
func (p *Promise) Get(name string) Value {
	if v, ok := p.bm[name]; ok {
		return v
	}
	v := p.getMethod(name)
	if v.IsFunc() {
		if p.bm == nil {
			p.bm = make(map[string]Value, 3)
		}
		p.bm[name] = v
	}
	return v
}

func (p *Promise) getMethod(name string) Value {
	switch name {
	case "then":
		return builtinFn("then", func(args []Value) Value {
			var onFul, onRej Value = Undef(), Undef()
			if len(args) > 0 {
				onFul = args[0]
			}
			if len(args) > 1 {
				onRej = args[1]
			}
			return PromiseVal(p.then(onFul, onRej))
		})
	case "catch":
		return builtinFn("catch", func(args []Value) Value {
			var onRej Value = Undef()
			if len(args) > 0 {
				onRej = args[0]
			}
			return PromiseVal(p.then(Undef(), onRej))
		})
	case "finally":
		return builtinFn("finally", func(args []Value) Value {
			var cb Value = Undef()
			if len(args) > 0 {
				cb = args[0]
			}
			return PromiseVal(p.finally(cb))
		})
	}
	return Undef()
}

func (p *Promise) Set(string, Value) {}
func (p *Promise) Str() string       { return "Promise" }
func (p *Promise) Type() string      { return "object" }

func (p *Promise) then(onFul, onRej Value) *Promise {
	sub := newPromise(p.vm)
	p.subscribe(onFul, onRej, sub)
	return sub
}

// finally runs cb after settlement, preserving the outcome.
func (p *Promise) finally(cb Value) *Promise {
	sub := newPromise(p.vm)
	p.subscribeFn(func(v Value, rej bool) {
		if !isCallable(cb) {
			if rej {
				sub.reject(v)
			} else {
				sub.resolve(v)
			}
			return
		}
		res, err := p.vm.callCallable(cb, nil)
		if err != nil {
			sub.reject(errToValue(err))
			return
		}
		if sp := res.AsPromise(); sp != nil {
			sp.subscribeFn(func(_ Value, inner bool) {
				if rej {
					sub.reject(v)
				} else {
					sub.resolve(v)
				}
			}, nil)
			return
		}
		if rej {
			sub.reject(v)
		} else {
			sub.resolve(v)
		}
	}, nil)
	return sub
}

// ---- the Promise constructor value ----

// promiseCtor is the `Promise` global: callable only with `new`, and carrying
// the static helpers.
type promiseCtor struct{ vm *VM }

func PromiseCtor(vm *VM) Value { return Host(&promiseCtor{vm: vm}) }

func (c *promiseCtor) Call([]Value) Value {
	return strErr("TypeError", "Promise constructor cannot be called without 'new'")
}

// New builds a promise and runs its executor, rejecting it if the executor
// throws.
func (c *promiseCtor) New(args []Value) Value {
	p := newPromise(c.vm)
	if len(args) == 0 || !isCallable(args[0]) {
		return PromiseVal(p)
	}
	resolve := builtinFn("resolve", func(a []Value) Value {
		v := Undef()
		if len(a) > 0 {
			v = a[0]
		}
		p.resolve(v)
		return Undef()
	})
	reject := builtinFn("reject", func(a []Value) Value {
		v := Undef()
		if len(a) > 0 {
			v = a[0]
		}
		p.reject(v)
		return Undef()
	})
	if _, err := c.vm.callCallable(args[0], []Value{resolve, reject}); err != nil {
		p.reject(errToValue(err))
	}
	return PromiseVal(p)
}

func (c *promiseCtor) Get(name string) Value {
	switch name {
	case "resolve":
		return builtinFn("Promise.resolve", func(args []Value) Value {
			v := Undef()
			if len(args) > 0 {
				v = args[0]
			}
			p := newPromise(c.vm)
			p.resolve(v)
			return PromiseVal(p)
		})
	case "reject":
		return builtinFn("Promise.reject", func(args []Value) Value {
			v := Undef()
			if len(args) > 0 {
				v = args[0]
			}
			p := newPromise(c.vm)
			p.reject(v)
			return PromiseVal(p)
		})
	case "all":
		return builtinFn("Promise.all", func(args []Value) Value { return c.all(args, false) })
	case "allSettled":
		return builtinFn("Promise.allSettled", func(args []Value) Value { return c.allSettled(args) })
	case "race":
		return builtinFn("Promise.race", func(args []Value) Value { return c.race(args) })
	case "any":
		return builtinFn("Promise.any", func(args []Value) Value { return c.any(args) })
	}
	return Undef()
}

func (c *promiseCtor) Set(string, Value) {}
func (c *promiseCtor) Str() string       { return "function Promise() { [native code] }" }
func (c *promiseCtor) Type() string      { return "function" }

func iterableOf(v Value) []Value {
	if a := v.AsArr(); a != nil {
		return a.elems
	}
	return nil
}

func (c *promiseCtor) all(args []Value, _ bool) Value {
	items := []Value{}
	if len(args) > 0 {
		items = iterableOf(args[0])
	}
	if items == nil {
		return strErr("TypeError", "Promise.all requires an iterable")
	}
	out := newPromise(c.vm)
	if len(items) == 0 {
		out.resolve(ArrVal(NewArr(0)))
		return PromiseVal(out)
	}
	arr := NewArr(len(items))
	left := len(items)
	for i, it := range items {
		i, it := i, it
		p := newPromise(c.vm)
		p.resolve(it)
		p.subscribeFn(func(v Value, rej bool) {
			if rej {
				out.reject(v)
				return
			}
			arr.elems[i] = v
			left--
			if left == 0 {
				out.resolve(ArrVal(arr))
			}
		}, nil)
	}
	return PromiseVal(out)
}

func (c *promiseCtor) allSettled(args []Value) Value {
	items := []Value{}
	if len(args) > 0 {
		items = iterableOf(args[0])
	}
	if items == nil {
		return strErr("TypeError", "Promise.allSettled requires an iterable")
	}
	out := newPromise(c.vm)
	if len(items) == 0 {
		out.resolve(ArrVal(NewArr(0)))
		return PromiseVal(out)
	}
	arr := NewArr(len(items))
	left := len(items)
	for i, it := range items {
		i, it := i, it
		p := newPromise(c.vm)
		p.resolve(it)
		p.subscribeFn(func(v Value, rej bool) {
			rec := NewObj()
			if rej {
				rec.set("status", Str("rejected"))
				rec.set("reason", v)
			} else {
				rec.set("status", Str("fulfilled"))
				rec.set("value", v)
			}
			arr.elems[i] = ObjVal(rec)
			left--
			if left == 0 {
				out.resolve(ArrVal(arr))
			}
		}, nil)
	}
	return PromiseVal(out)
}

func (c *promiseCtor) race(args []Value) Value {
	items := []Value{}
	if len(args) > 0 {
		items = iterableOf(args[0])
	}
	if items == nil {
		return strErr("TypeError", "Promise.race requires an iterable")
	}
	out := newPromise(c.vm)
	for _, it := range items {
		p := newPromise(c.vm)
		p.resolve(it)
		p.subscribeFn(func(v Value, rej bool) {
			if rej {
				out.reject(v)
			} else {
				out.resolve(v)
			}
		}, nil)
	}
	return PromiseVal(out)
}

func (c *promiseCtor) any(args []Value) Value {
	items := []Value{}
	if len(args) > 0 {
		items = iterableOf(args[0])
	}
	if items == nil {
		return strErr("TypeError", "Promise.any requires an iterable")
	}
	out := newPromise(c.vm)
	if len(items) == 0 {
		out.reject(strErr("AggregateError", "All promises were rejected"))
		return PromiseVal(out)
	}
	left := len(items)
	for _, it := range items {
		p := newPromise(c.vm)
		p.resolve(it)
		p.subscribeFn(func(v Value, rej bool) {
			if !rej {
				out.resolve(v)
				return
			}
			left--
			if left == 0 {
				out.reject(strErr("AggregateError", "All promises were rejected"))
			}
		}, nil)
	}
	return PromiseVal(out)
}

// builtinFn wraps a Go function as a callable value.
func builtinFn(name string, fn func([]Value) Value) Value {
	return Value{tag: tFunc, ref: &builtin{fn: fn, name: name}}
}

// methodFn builds a builtin whose first argument is the receiver.
func methodFn(name string, fn func([]Value) Value) Value {
	return Value{tag: tFunc, ref: &builtin{fn: fn, name: name, recvFirst: true}}
}
