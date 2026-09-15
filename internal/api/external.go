package api

import "context"

// Which listener a request arrived on.
//
// The internal one serves the whole app on the LAN. The external one is
// the internet-facing surface, with every admin route absent rather than
// refused — see ExternalHandler.
//
// The app needs to know which it is talking to. An admin signing in from
// outside would otherwise be shown the settings and users tabs, whose
// every call answers 404 because those routes do not exist there. That
// reads as a broken install rather than as a boundary doing its job.

type externalKey struct{}

func markExternal(ctx context.Context) context.Context {
	return context.WithValue(ctx, externalKey{}, true)
}

// isExternal reports whether this request came in from the internet-facing
// listener.
func isExternal(ctx context.Context) bool {
	v, _ := ctx.Value(externalKey{}).(bool)
	return v
}
