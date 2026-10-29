package deploy

import (
	"context"
	"net"
	"testing"
)

func TestDialPort(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, open, _ := net.SplitHostPort(l.Addr().String())
	l.Close() // now refused
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, true); lv != Warn {
		t.Errorf("closed port, installed: %v %s", lv, d)
	}
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, false); lv != Pass {
		t.Errorf("closed port, not installed: %v %s", lv, d)
	}
	l, _ = net.Listen("tcp", "127.0.0.1:"+open)
	defer l.Close()
	if lv, d := dialPort(context.Background(), "127.0.0.1", open, true); lv != Pass || d != "open" {
		t.Errorf("open port: %v %s", lv, d)
	}
}
