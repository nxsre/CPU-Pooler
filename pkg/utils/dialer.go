package utils

import (
	"context"
	"fmt"
	"github.com/golang/glog"
	"net"
	"net/url"
	"strings"
	"time"
)

// ParseDialTarget returns the network and address to pass to dialer.
func ParseDialTarget(target string) (string, string) {
	glog.Warning("ParseDialTarget:", target)
	net := "tcp"
	m1 := strings.Index(target, ":")
	m2 := strings.Index(target, ":/")
	// handle unix:addr which will fail with url.Parse
	if m1 >= 0 && m2 < 0 {
		if n := target[0:m1]; n == "unix" {
			return n, target[m1+1:]
		}
	}
	if m2 >= 0 {
		t, err := url.Parse(target)
		if err != nil {
			return net, target
		}
		scheme := t.Scheme
		addr := t.Path
		if scheme == "unix" {
			if addr == "" {
				addr = t.Host
			}
			return scheme, addr
		}
	}
	glog.Warning("ParseDialTarget:", net, "#", target)
	return net, target
}

type MyDialer interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
}

type myDialer struct {
	dialer  *net.Dialer
	timeout time.Duration
	retry   int
}

func (t *myDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	var conn net.Conn
	var err error
	for retry := t.retry; retry > 0; retry-- {
		dialer := net.Dialer{Timeout: t.timeout}
		var err error
		if conn, err = dialer.DialContext(ctx, network, address); err != nil {
			if retry == 1 {
				return nil, fmt.Errorf("Could not dial URL %s: %s", address, err)
			}
		} else {
			retry = 0
		}

	}
	return conn, err
}

type optFunc func(*myDialer)

func (f optFunc) apply(d *myDialer) {
	f(d)
}

type Option interface{ apply(*myDialer) }

func RetryOption(r int) Option {
	return optFunc(func(d *myDialer) {
		d.retry = r
	})
}

func TimeoutOption(timeout time.Duration) Option {
	return optFunc(func(d *myDialer) {
		d.timeout = timeout
	})
}

func DialerOption(dialer *net.Dialer) Option {
	return optFunc(func(d *myDialer) {
		d.dialer = dialer
	})
}

func NewMyDialer(opts ...Option) MyDialer {
	// 默认值
	d := &myDialer{
		timeout: time.Second,
		retry:   2,
	}
	// 接下来用传入的 Option 修改默认值，如果不需要修改默认值，
	// 就不需要传入对应的 Option
	for _, opt := range opts {
		opt.apply(d)
	}
	// 最后再检查一下，如果 Option 没有传入自定义的必要字段，我
	// 们在这里补一下。
	if d.dialer == nil {
		d.dialer = &net.Dialer{}
	}
	return d
}

// 提供单独的方法，并随接口导出，提供类似 Set 模式的功能。
func (d *myDialer) ApplyOptions(opts ...Option) {
	for _, opt := range opts {
		opt.apply(d)
	}
}
