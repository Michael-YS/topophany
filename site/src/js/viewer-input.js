// Bound work before it crosses the worker boundary. One batch produces one
// frame; resize and repeated wheel events need only their latest state.
export class ViewerInput {
  constructor() {
    this.keys = [];
    this.resized = false;
    this.resumed = false;
  }

  key(value) {
    const wheel = (key) => key === 'wheel-up' || key === 'wheel-down';
    // Preserve typed text and command ordering, but don't make quit wait for
    // old scrolling. q is still interpreted by Go (it may be search text).
    if (value === 'q') this.keys = this.keys.filter((key) => !wheel(key));
    if (wheel(value) && wheel(this.keys.at(-1))) this.keys.pop();
    if (this.keys.length < 64) this.keys.push(value.slice(0, 8198));
  }

  next() {
    const keys = this.keys.splice(0, 16);
    const action = keys.length ? 'input' : this.resized || this.resumed ? 'resize' : 'tick';
    const resume = this.resumed;
    this.resized = this.resumed = false;
    return { action, keys, resume };
  }
}
