package xmpp

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/vectorcore/cbc/internal/cap"
	"github.com/vectorcore/cbc/internal/config"
)

const (
	nsStream = "http://etherx.jabber.org/streams"
	nsClient = "jabber:client"
	nsTLS    = "urn:ietf:params:xml:ns:xmpp-tls"
	nsSASL   = "urn:ietf:params:xml:ns:xmpp-sasl"
	nsBind   = "urn:ietf:params:xml:ns:xmpp-bind"
)

type Client struct {
	cfg     config.CBE
	onAlert func(cap.Alert) error
	onState func(bool, error)
}

func New(cfg config.CBE, onAlert func(cap.Alert) error, onState func(bool, error)) *Client {
	return &Client{cfg: cfg, onAlert: onAlert, onState: onState}
}

func (c *Client) Run(ctx context.Context) {
	backoff := c.cfg.ReconnectMin
	for ctx.Err() == nil {
		err := c.runOnce(ctx)
		c.onState(false, err)
		if ctx.Err() != nil {
			return
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		backoff *= 2
		if backoff > c.cfg.ReconnectMax {
			backoff = c.cfg.ReconnectMax
		}
	}
}

func (c *Client) runOnce(ctx context.Context) error {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", c.cfg.Address)
	if err != nil {
		return fmt.Errorf("dial CBE: %w", err)
	}
	defer conn.Close()
	if c.cfg.TLSMode == "direct_tls" {
		conn = tls.Client(conn, c.tlsConfig())
		if err := conn.(*tls.Conn).HandshakeContext(ctx); err != nil {
			return fmt.Errorf("CBE TLS handshake: %w", err)
		}
	}
	dec := xml.NewDecoder(conn)
	if err := c.openStream(conn, dec); err != nil {
		return err
	}
	if c.cfg.TLSMode == "plain" {
		if err := requireFeature(dec, "starttls", nsTLS); err != nil {
			return err
		}
		if _, err := io.WriteString(conn, `<starttls xmlns="`+nsTLS+`"/>`); err != nil {
			return err
		}
		if err := expectStart(dec, "proceed", nsTLS); err != nil {
			return err
		}
		tlsConn := tls.Client(conn, c.tlsConfig())
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("CBE STARTTLS handshake: %w", err)
		}
		conn = tlsConn
		dec = xml.NewDecoder(conn)
		if err := c.openStream(conn, dec); err != nil {
			return err
		}
	}
	// The server sends a fresh <stream:features> advertising SASL mechanisms
	// immediately after any stream restart - both the initial one (direct_tls)
	// and the one following STARTTLS (plain) - and it must be consumed before
	// sending <auth>, or the decoder's next read sees that stray <features>
	// instead of the expected SASL response.
	if err := skipFeatures(dec); err != nil {
		return err
	}
	plain := base64.StdEncoding.EncodeToString([]byte("\x00" + c.cfg.Username + "\x00" + c.cfg.Password))
	if _, err := io.WriteString(conn, `<auth xmlns="`+nsSASL+`" mechanism="PLAIN">`+plain+`</auth>`); err != nil {
		return err
	}
	if err := expectStart(dec, "success", nsSASL); err != nil {
		return err
	}
	if err := dec.Skip(); err != nil {
		return err
	}
	if err := c.openStream(conn, dec); err != nil {
		return err
	}
	if err := skipFeatures(dec); err != nil {
		return err
	}
	if _, err := io.WriteString(conn, `<iq type="set" id="bind-1"><bind xmlns="`+nsBind+`"><resource>`+xmlEscape(c.cfg.Resource)+`</resource></bind></iq>`); err != nil {
		return err
	}
	if err := expectStart(dec, "iq", nsClient); err != nil {
		return err
	}
	if err := dec.Skip(); err != nil {
		return err
	}
	c.onState(true, nil)
	for {
		var raw struct {
			Alert *cap.Alert `xml:"urn:oasis:names:tc:emergency:cap:1.2 alert"`
		}
		start, err := nextStart(dec)
		if err != nil {
			return err
		}
		if start.Name.Local != "message" {
			if err := dec.Skip(); err != nil {
				return err
			}
			continue
		}
		if err := dec.DecodeElement(&raw, &start); err != nil {
			return err
		}
		if raw.Alert != nil {
			if err := raw.Alert.Validate(); err != nil {
				return err
			}
			if err := c.onAlert(*raw.Alert); err != nil {
				return err
			}
		}
	}
}

func (c *Client) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.cfg.Domain, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.cfg.InsecureSkipVerify}
} // #nosec G402 -- operator-controlled test setting.
func (c *Client) openStream(w io.Writer, d *xml.Decoder) error {
	if _, err := fmt.Fprintf(w, `<?xml version="1.0"?><stream:stream to="%s" xmlns="%s" xmlns:stream="%s" version="1.0">`, xmlEscape(c.cfg.Domain), nsClient, nsStream); err != nil {
		return err
	}
	if err := expectStart(d, "stream", nsStream); err != nil {
		return err
	}
	return nil
}
func nextStart(d *xml.Decoder) (xml.StartElement, error) {
	for {
		t, err := d.Token()
		if err != nil {
			return xml.StartElement{}, err
		}
		if s, ok := t.(xml.StartElement); ok {
			return s, nil
		}
	}
}
func expectStart(d *xml.Decoder, local, space string) error {
	s, err := nextStart(d)
	if err != nil {
		return err
	}
	if s.Name.Local != local || s.Name.Space != space {
		return fmt.Errorf("XMPP: expected {%s}%s, got {%s}%s", space, local, s.Name.Space, s.Name.Local)
	}
	return nil
}
func skipFeatures(d *xml.Decoder) error {
	if err := expectStart(d, "features", nsStream); err != nil {
		return err
	}
	return d.Skip()
}
func requireFeature(d *xml.Decoder, local, space string) error {
	if err := expectStart(d, "features", nsStream); err != nil {
		return err
	}
	found := false
	for {
		t, err := d.Token()
		if err != nil {
			return err
		}
		switch v := t.(type) {
		case xml.StartElement:
			if v.Name.Local == local && v.Name.Space == space {
				found = true
			}
			if err := d.Skip(); err != nil {
				return err
			}
		case xml.EndElement:
			if v.Name.Local == "features" && v.Name.Space == nsStream {
				if !found {
					return fmt.Errorf("XMPP: CBE does not offer %s", local)
				}
				return nil
			}
		}
	}
}
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
