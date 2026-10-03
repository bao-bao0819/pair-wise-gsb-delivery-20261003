package milter

import (
	"bytes"
	"net"
	"reflect"
	"testing"
)

type macroMilter struct {
	NoOpMilter

	onConnect func(m *Modifier)
	onHelo    func(m *Modifier)
	onMail    func(m *Modifier)
	onRcpt    func(m *Modifier)
	onBody    func(m *Modifier)
	onAbort   func(m *Modifier)
}

func (mm *macroMilter) Connect(host string, family string, port uint16, addr net.IP, m *Modifier) (Response, error) {
	if mm.onConnect != nil {
		mm.onConnect(m)
	}
	return RespContinue, nil
}

func (mm *macroMilter) Helo(name string, m *Modifier) (Response, error) {
	if mm.onHelo != nil {
		mm.onHelo(m)
	}
	return RespContinue, nil
}

func (mm *macroMilter) MailFrom(from string, m *Modifier) (Response, error) {
	if mm.onMail != nil {
		mm.onMail(m)
	}
	return RespContinue, nil
}

func (mm *macroMilter) RcptTo(rcptTo string, m *Modifier) (Response, error) {
	if mm.onRcpt != nil {
		mm.onRcpt(m)
	}
	return RespContinue, nil
}

func (mm *macroMilter) Body(m *Modifier) (Response, error) {
	if mm.onBody != nil {
		mm.onBody(m)
	}
	return RespAccept, nil
}

func (mm *macroMilter) Abort(m *Modifier) error {
	if mm.onAbort != nil {
		mm.onAbort(m)
	}
	return nil
}

// TestMilterClient_MacroStagesFlow drives two messages over a single
// connection and verifies that macros are stored per stage, merged with
// connection-level macros for callbacks, and cleaned up by message end,
// Abort and QuitNewConn.
func TestMilterClient_MacroStagesFlow(t *testing.T) {
	var (
		connectMacros []map[string]string
		heloMacros    []map[string]string
		mailMacros    []map[string]string
		rcptMacros    map[string]string
		bodyMacros    map[string]string
		abortMacros   map[string]string

		heloStages map[string]Code
		mailStages map[string]Code
	)
	stageOf := func(m *Modifier, names ...string) map[string]Code {
		stages := make(map[string]Code, len(names))
		for _, name := range names {
			if stage, ok := m.MacroStage(name); ok {
				stages[name] = stage
			}
		}
		return stages
	}

	mm := &macroMilter{
		onConnect: func(m *Modifier) {
			connectMacros = append(connectMacros, m.Macros)
		},
		onHelo: func(m *Modifier) {
			// record a copy so the mutation check below does not alter it
			cp := make(map[string]string, len(m.Macros))
			for k, v := range m.Macros {
				cp[k] = v
			}
			heloMacros = append(heloMacros, cp)
			if heloStages == nil {
				heloStages = stageOf(m, "daemon_name", "shared", "tls_version", "mail_id", "missing")
			}
			// mutating the map handed to the callback must not corrupt
			// the internal per-stage state
			delete(m.Macros, "daemon_name")
			m.Macros["junk"] = "junk"
		},
		onMail: func(m *Modifier) {
			mailMacros = append(mailMacros, m.Macros)
			if mailStages == nil {
				mailStages = stageOf(m, "daemon_name", "shared", "mail_id", "tls_version")
			}
		},
		onRcpt: func(m *Modifier) {
			rcptMacros = m.Macros
		},
		onBody: func(m *Modifier) {
			bodyMacros = m.Macros
		},
		onAbort: func(m *Modifier) {
			abortMacros = m.Macros
		},
	}
	s := Server{
		NewMilter: func() Milter {
			return mm
		},
	}
	defer s.Close()
	local, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(local)

	cl := NewClientWithOptions("tcp", local.Addr().String(), ClientOptions{
		ActionMask: OptAddHeader | OptChangeHeader,
	})
	defer cl.Close()
	session, err := cl.Session()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	assertMacros := func(context string, got, want map[string]string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wrong macros, got %v, want %v", context, got, want)
		}
	}
	assertStages := func(context string, got, want map[string]Code) {
		t.Helper()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: wrong macro stages, got %v, want %v", context, got, want)
		}
	}

	// first message
	if _, err := session.Conn("host", FamilyInet, 25565, "172.0.0.1"); err != nil {
		t.Fatal(err)
	}
	assertMacros("connect", connectMacros[0], map[string]string{})

	if err := session.Macros(CodeConn, "daemon_name", "mx1", "shared", "conn"); err != nil {
		t.Fatal(err)
	}
	if err := session.Macros(CodeHelo, "tls_version", "TLSv1.3"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Helo("helo1"); err != nil {
		t.Fatal(err)
	}
	assertMacros("helo", heloMacros[0], map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
		"tls_version": "TLSv1.3",
	})
	assertStages("helo", heloStages, map[string]Code{
		"daemon_name": CodeConn,
		"shared":      CodeConn,
		"tls_version": CodeHelo,
	})

	// same-stage definitions override earlier ones
	if err := session.Macros(CodeMail, "mail_id", "m1", "shared", "mail1"); err != nil {
		t.Fatal(err)
	}
	if err := session.Macros(CodeMail, "mail_id", "m1-final"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Mail("from@example.org", nil); err != nil {
		t.Fatal(err)
	}
	assertMacros("mail", mailMacros[0], map[string]string{
		"daemon_name": "mx1",
		"shared":      "mail1",
		"mail_id":     "m1-final",
	})
	assertStages("mail", mailStages, map[string]Code{
		"daemon_name": CodeConn,
		"shared":      CodeMail,
		"mail_id":     CodeMail,
	})

	if err := session.Macros(CodeRcpt, "rcpt_mailer", "local"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Rcpt("to@example.org", nil); err != nil {
		t.Fatal(err)
	}
	// the rcpt stage must not see the temporary macros of the mail callback
	assertMacros("rcpt", rcptMacros, map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
		"rcpt_mailer": "local",
	})

	if _, _, err := session.BodyReadFrom(bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
	assertMacros("body", bodyMacros, map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
	})

	// second message on the same connection
	if err := session.Macros(CodeMail, "mail_id", "m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Mail("from2@example.org", nil); err != nil {
		t.Fatal(err)
	}
	// message-scoped macros of the first message are gone
	assertMacros("mail2", mailMacros[1], map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
		"mail_id":     "m2",
	})

	if err := session.Abort(); err != nil {
		t.Fatal(err)
	}

	// SMFIC_ABORT has no response; the HELO round-trip below also
	// synchronizes with the server processing the abort.
	if _, err := session.Helo("helo2"); err != nil {
		t.Fatal(err)
	}
	// the abort callback still sees the macros of the aborted message
	assertMacros("abort", abortMacros, map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
		"mail_id":     "m2",
	})
	// abort resets to before HELO: helo and message macros are cleared,
	// connection-level macros survive
	assertMacros("helo2", heloMacros[1], map[string]string{
		"daemon_name": "mx1",
		"shared":      "conn",
	})

	// QUIT_NC announces a new connection on the same socket
	if err := writePacket(session.conn, &Message{Code: byte(CodeQuitNewConn)}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Conn("host2", FamilyInet, 25566, "172.0.0.2"); err != nil {
		t.Fatal(err)
	}
	assertMacros("connect2", connectMacros[1], map[string]string{})
}
