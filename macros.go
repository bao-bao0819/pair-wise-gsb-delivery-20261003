package milter

// macroStore keeps macros separated by the protocol stage (SMFIC_MACRO
// cmdcode) they were defined for, so that callbacks can be presented with a
// deterministic merge of connection-level and current-stage macros.
type macroStore struct {
	byStage map[Code]map[string]string
}

func newMacroStore() *macroStore {
	return &macroStore{byStage: make(map[Code]map[string]string)}
}

// define merges the given name/value pairs into the stage, overriding values
// defined earlier for the same stage.
func (s *macroStore) define(stage Code, kv map[string]string) {
	if len(kv) == 0 {
		return
	}
	m := s.byStage[stage]
	if m == nil {
		m = make(map[string]string, len(kv))
		s.byStage[stage] = m
	}
	for k, v := range kv {
		m[k] = v
	}
}

// snapshot returns the deterministic merge of connection-level macros and
// macros defined for the given stage. Stage values take precedence over
// connection-level ones. The returned map is a copy and can be freely
// modified by the caller without affecting the store.
func (s *macroStore) snapshot(stage Code) map[string]string {
	merged := make(map[string]string)
	for k, v := range s.byStage[CodeConn] {
		merged[k] = v
	}
	if stage != CodeConn {
		for k, v := range s.byStage[stage] {
			merged[k] = v
		}
	}
	return merged
}

// stageOf reports the stage a macro visible in the snapshot of the given
// stage comes from. Macros of the current stage shadow connection-level
// ones, just like in snapshot.
func (s *macroStore) stageOf(stage Code, name string) (Code, bool) {
	if stage != CodeConn {
		if _, ok := s.byStage[stage][name]; ok {
			return stage, true
		}
	}
	if _, ok := s.byStage[CodeConn][name]; ok {
		return CodeConn, true
	}
	return 0, false
}

// clearMessage drops macros of message-scoped stages. Connection and HELO
// stage macros are preserved for the next message of the same SMTP session.
func (s *macroStore) clearMessage() {
	for stage := range s.byStage {
		if stage != CodeConn && stage != CodeHelo {
			delete(s.byStage, stage)
		}
	}
}

// clearAbort resets the store to the state before SMFIC_HELO, as required
// for SMFIC_ABORT: only connection-level macros are preserved.
func (s *macroStore) clearAbort() {
	for stage := range s.byStage {
		if stage != CodeConn {
			delete(s.byStage, stage)
		}
	}
}

// clearAll drops all macros, including connection-level ones. Used when the
// MTA announces a new connection on the same socket (SMFIC_QUIT_NC).
func (s *macroStore) clearAll() {
	for stage := range s.byStage {
		delete(s.byStage, stage)
	}
}
