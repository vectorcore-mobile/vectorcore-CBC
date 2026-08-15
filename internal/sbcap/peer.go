package sbcap

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/ishidawataru/sctp"
)

// Exchange writes one complete SBcAP SCTP user message and verifies that the
// peer returned the matching successful-outcome procedure.
func Exchange(ctx context.Context, conn net.Conn, request []byte, procedure int) error {
	return ExchangeFor(ctx, conn, request, procedure, 0, 0)
}
func ExchangeFor(ctx context.Context, conn net.Conn, request []byte, procedure int, messageID, serial uint16) error {
	if err := conn.SetWriteDeadline(deadline(ctx)); err != nil {
		return err
	}
	if n, err := conn.Write(request); err != nil {
		return fmt.Errorf("send SBcAP procedure %d: %w", procedure, err)
	} else if n != len(request) {
		return fmt.Errorf("send SBcAP procedure %d: short write %d/%d", procedure, n, len(request))
	}
	if err := conn.SetReadDeadline(deadline(ctx)); err != nil {
		return err
	}
	buf := make([]byte, 16*1024)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("read SBcAP procedure %d response: %w", procedure, err)
	}
	outcome, gotProcedure, err := Header(buf[:n])
	if err != nil {
		return err
	}
	if gotProcedure != procedure || outcome != OutcomeSuccessful {
		return fmt.Errorf("SBcAP procedure %d response is outcome %d procedure %d", procedure, outcome, gotProcedure)
	}
	if messageID != 0 || serial != 0 {
		gotID, gotSerial, err := ResponseIDs(buf[:n], procedure)
		if err != nil {
			return err
		}
		if gotID != messageID || gotSerial != serial {
			return fmt.Errorf("SBcAP response correlation mismatch: message ID %#04x/%#04x serial %#04x/%#04x", gotID, messageID, gotSerial, serial)
		}
	}
	return nil
}

func deadline(ctx context.Context) time.Time {
	if d, ok := ctx.Deadline(); ok {
		return d
	}
	return time.Now().Add(10 * time.Second)
}

// DialSCTP establishes one semipermanent SCTP association to an LTE MME.
// localAddress, if non-empty, pins the association to that single local IP
// instead of letting the OS multi-home across every local interface -
// required by MMEs that authorize CBC peers by a single source IP (a
// multi-homed association reports as e.g. "ip1/ip2/ip3" on the MME side,
// which never matches such an allowlist).
func DialSCTP(ctx context.Context, localAddress, address string) (*sctp.SCTPConn, error) {
	raddr, err := sctp.ResolveSCTPAddr("sctp", address)
	if err != nil {
		return nil, fmt.Errorf("resolve MME SCTP address %q: %w", address, err)
	}
	var laddr *sctp.SCTPAddr
	if localAddress != "" {
		laddr, err = sctp.ResolveSCTPAddr("sctp", net.JoinHostPort(localAddress, "0"))
		if err != nil {
			return nil, fmt.Errorf("resolve local SBcAP bind address %q: %w", localAddress, err)
		}
	}
	type result struct {
		conn *sctp.SCTPConn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		conn, err := (&sctp.SocketConfig{InitMsg: sctp.InitMsg{NumOstreams: 1, MaxInstreams: 1}}).Dial("sctp", laddr, raddr)
		done <- result{conn, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, fmt.Errorf("dial MME SCTP %q: %w", address, r.err)
		}
		return r.conn, nil
	case <-ctx.Done():
		go func() {
			if r := <-done; r.conn != nil {
				_ = r.conn.Close()
			}
		}()
		return nil, ctx.Err()
	}
}
