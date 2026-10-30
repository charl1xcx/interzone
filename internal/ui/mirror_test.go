package ui

import "context"

// Whether this machine runs interzone-mirror's node mustn't change the tests.
func init() { findLocalMirror = func(context.Context) string { return "" } }
