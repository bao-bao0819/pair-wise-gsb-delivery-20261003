// Modifier instance is provided to milter handlers to modify email messages

package milter

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/textproto"
)

// postfix wants LF lines endings. Using CRLF results in double CR sequences.
func crlfToLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte{'\r', '\n'}, []byte{'\n'})
}

// Modifier provides access to Macros, Headers and Body data to callback handlers. It also defines a
// number of functions that can be used by callback handlers to modify processing of the email message
type Modifier struct {
	// Macros contains connection-level macros merged with the macros
	// defined for the current protocol stage. It is a copy and can be
	// modified freely.
	Macros  map[string]string
	Headers textproto.MIMEHeader

	macroStages map[string]Stage

	writePacket func(*Message) error
}

// MacroStage reports the protocol stage at which the given macro was
// defined. The second return value is false if the macro is not defined.
func (m *Modifier) MacroStage(name string) (Stage, bool) {
	stage, ok := m.macroStages[name]
	return stage, ok
}

// AddRecipient appends a new envelope recipient for current message
func (m *Modifier) AddRecipient(r string) error {
	data := []byte(fmt.Sprintf("<%s>", r) + null)
	return m.writePacket(NewResponse('+', data).Response())
}

// DeleteRecipient removes an envelope recipient address from message
func (m *Modifier) DeleteRecipient(r string) error {
	data := []byte(fmt.Sprintf("<%s>", r) + null)
	return m.writePacket(NewResponse('-', data).Response())
}

// ReplaceBody substitutes message body with provided body
func (m *Modifier) ReplaceBody(body []byte) error {
	body = crlfToLF(body)
	return m.writePacket(NewResponse('b', body).Response())
}

// AddHeader appends a new email message header the message
func (m *Modifier) AddHeader(name, value string) error {
	var buffer bytes.Buffer
	buffer.WriteString(name + null)
	buffer.Write(crlfToLF([]byte(value)))
	buffer.WriteString(null)
	return m.writePacket(NewResponse('h', buffer.Bytes()).Response())
}

// Quarantine a message by giving a reason to hold it
func (m *Modifier) Quarantine(reason string) error {
	return m.writePacket(NewResponse('q', []byte(reason+null)).Response())
}

// ChangeHeader replaces the header at the specified position with a new one.
// The index is per name.
func (m *Modifier) ChangeHeader(index int, name, value string) error {
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.BigEndian, uint32(index)); err != nil {
		return err
	}
	buffer.WriteString(name + null)
	buffer.Write(crlfToLF([]byte(value)))
	buffer.WriteString(null)
	return m.writePacket(NewResponse('m', buffer.Bytes()).Response())
}

// InsertHeader inserts the header at the specified position
func (m *Modifier) InsertHeader(index int, name, value string) error {
	var buffer bytes.Buffer
	if err := binary.Write(&buffer, binary.BigEndian, uint32(index)); err != nil {
		return err
	}
	buffer.WriteString(name + null)
	buffer.Write(crlfToLF([]byte(value)))
	buffer.WriteString(null)
	return m.writePacket(NewResponse('i', buffer.Bytes()).Response())
}

// ChangeFrom replaces the FROM envelope header with a new one
func (m *Modifier) ChangeFrom(value string) error {
	data := []byte(value + null)
	return m.writePacket(NewResponse('e', data).Response())
}

// newModifier creates a new Modifier instance from milterSession
func newModifier(s *milterSession) *Modifier {
	return &Modifier{
		Macros:      s.mergedMacros(),
		macroStages: s.macroStages(),
		Headers:     s.headers,
		writePacket: s.WritePacket,
	}
}
