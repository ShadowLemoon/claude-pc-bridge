package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"golang.org/x/net/proxy"
)

// dial 连接 addr。proxyURL 为空时直接连，否则走 socks5:// 或 http:// 代理
func dial(proxyURL, addr string, timeout time.Duration) (net.Conn, error) {
	direct := &net.Dialer{Timeout: timeout}
	if proxyURL == "" {
		return direct.Dial("tcp", addr)
	}
	u, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("代理地址格式错误: %w", err)
	}
	switch u.Scheme {
	case "socks5", "socks5h":
		d, err := proxy.FromURL(u, direct)
		if err != nil {
			return nil, err
		}
		conn, err := d.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("通过代理 %s 连接失败: %w", u.Host, err)
		}
		return conn, nil
	case "http":
		return dialHTTPConnect(direct, u, addr, timeout)
	default:
		return nil, fmt.Errorf("不支持的代理类型 %q，只支持 socks5:// 和 http://", u.Scheme)
	}
}

func dialHTTPConnect(d *net.Dialer, u *url.URL, addr string, timeout time.Duration) (net.Conn, error) {
	conn, err := d.Dial("tcp", u.Host)
	if err != nil {
		return nil, fmt.Errorf("连接代理 %s 失败: %w", u.Host, err)
	}
	_ = conn.SetDeadline(time.Now().Add(timeout))
	req := &http.Request{
		Method: http.MethodConnect,
		URL:    &url.URL{Opaque: addr},
		Host:   addr,
		Header: make(http.Header),
	}
	if u.User != nil {
		pass, _ := u.User.Password()
		req.SetBasicAuth(u.User.Username(), pass)
	}
	if err := req.Write(conn); err != nil {
		conn.Close()
		return nil, err
	}
	// 代理回复 200 后，后面就是 ssh 的数据。用 1 字节缓冲读响应头，避免多读走 ssh 数据
	br := bufio.NewReaderSize(conn, 1)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("代理 %s 没有正常回复: %w", u.Host, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		conn.Close()
		return nil, fmt.Errorf("代理 %s 拒绝连接: %s", u.Host, resp.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}
