'use strict';
// events — Node.js EventEmitter implementation.

class EventEmitter {
  constructor(opts) {
    if (opts && opts.captureRejections) this._captureRejections = true;
    this._events = Object.create(null);
    this._maxListeners = undefined;
  }

  _events_() { return this._events; }

  on(event, listener) {
    if (typeof listener !== 'function') throw new TypeError('listener must be a function');
    const ev = this._events;
    if (ev[event] === undefined) {
      ev[event] = listener;
      this._maybeWarnMaxListeners(event);
    } else if (Array.isArray(ev[event])) {
      ev[event].push(listener);
      this._maybeWarnMaxListeners(event);
    } else {
      ev[event] = [ev[event], listener];
    }
    return this;
  }

  addListener(event, listener) { return this.on(event, listener); }

  prependListener(event, listener) {
    if (typeof listener !== 'function') throw new TypeError('listener must be a function');
    const ev = this._events;
    if (ev[event] === undefined) {
      ev[event] = listener;
    } else if (Array.isArray(ev[event])) {
      ev[event].unshift(listener);
    } else {
      ev[event] = [listener, ev[event]];
    }
    return this;
  }

  once(event, listener) {
    if (typeof listener !== 'function') throw new TypeError('listener must be a function');
    const self = this;
    function wrapper(...args) {
      self.removeListener(event, wrapper);
      return listener.apply(self, args);
    }
    wrapper.listener = listener;
    this.on(event, wrapper);
    return this;
  }

  removeListener(event, listener) {
    const ev = this._events;
    let list = ev[event];
    if (list === undefined) return this;
    if (Array.isArray(list)) {
      for (let i = list.length - 1; i >= 0; i--) {
        if (list[i] === listener || list[i].listener === listener) {
          list.splice(i, 1);
          break;
        }
      }
      if (list.length === 1) ev[event] = list[0];
      else if (list.length === 0) delete ev[event];
    } else if (list === listener || list.listener === listener) {
      delete ev[event];
    }
    return this;
  }

  off(event, listener) { return this.removeListener(event, listener); }

  removeAllListeners(event) {
    if (event === undefined) {
      this._events = Object.create(null);
    } else {
      delete this._events[event];
    }
    return this;
  }

  setMaxListeners(n) { this._maxListeners = n; return this; }
  getMaxListeners() { return this._maxListeners === undefined ? 10 : this._maxListeners; }

  _maybeWarnMaxListeners(event) {
    const list = this._events[event];
    const n = Array.isArray(list) ? list.length : 1;
    if (n === this.getMaxListeners()) {
      process.emitWarning(`MaxListenersExceededWarning: Possible EventEmitter memory leak detected. ${n} ${String(event)} listeners added.`);
    }
  }

  listeners(event) {
    const list = this._events[event];
    if (list === undefined) return [];
    if (Array.isArray(list)) return list.slice();
    return [list];
  }

  rawListeners(event) { return this.listeners(event); }

  listenerCount(event) { return this.listeners(event).length; }

  eventNames() {
    return Object.keys(this._events);
  }

  emit(event, ...args) {
    const list = this._events[event];
    if (list === undefined) {
      if (event === 'error') {
        const err = args[0];
        if (err instanceof Error) throw err;
        throw new Error('Unhandled error. (' + String(err) + ')');
      }
      return false;
    }
    const handlers = Array.isArray(list) ? list.slice() : [list];
    for (const h of handlers) {
      h.apply(this, args);
    }
    return true;
  }
}

EventEmitter.EventEmitter = EventEmitter;
EventEmitter.usingDomains = false;
EventEmitter.defaultMaxListeners = 10;
EventEmitter.captureRejectionSymbol = Symbol('kCapture');
EventEmitter.errorMonitorSymbol = Symbol('kErrorMonitor');

EventEmitter.once = function (emitter, name) {
  return new Promise((resolve, reject) => {
    const errorListener = (err) => {
      emitter.removeListener(name, resolve);
      reject(err);
    };
    emitter.once(name, resolve);
    emitter.once('error', errorListener);
  });
};

// EventEmitter.on returns an async iterable over emitted events.
EventEmitter.on = function (emitter, name) {
  const state = {
    queue: [],
    waiting: [],
    error: null,
    finished: false,
  };
  const push = (event) => {
    if (state.waiting.length) state.waiting.shift()(event);
    else state.queue.push(event);
  };
  emitter.on(name, (event) => push(event));
  emitter.on('error', (err) => {
    state.error = err;
    if (state.waiting.length) {
      const w = state.waiting.shift();
      w(undefined);
    }
  });
  return {
    [Symbol.asyncIterator]() {
      return this;
    },
    async next() {
      if (state.error) {
        const e = state.error;
        state.error = null;
        throw e;
      }
      if (state.queue.length) return { value: state.queue.shift(), done: false };
      if (state.finished) return { value: undefined, done: true };
      return new Promise((resolve) => state.waiting.push((v) => resolve({ value: v, done: false })));
    },
  };
};

module.exports = EventEmitter;
module.exports.EventEmitter = EventEmitter;
module.exports.default = EventEmitter;
module.exports.once = EventEmitter.once;
module.exports.on = EventEmitter.on;
module.exports.getEventListeners = function (emitter, type) {
  return emitter.listeners(type);
};
module.exports.setMaxListeners = function (n, ...emitters) {
  for (const e of emitters) e.setMaxListeners(n);
};
module.exports.getMaxListeners = function (emitter) {
  return emitter.getMaxListeners();
};
