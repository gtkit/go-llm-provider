// Package einoadapter adapts one provider invocation to Eino v0.9.21.
// It does not execute tools, retry, route models, or record business usage.
// Models can be shared when the underlying Provider supports concurrent calls.
// Stream callers must close the Eino reader, then its per-call StreamControl.
package einoadapter
