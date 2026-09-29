package stage

import "github.com/tjjh89017/noahsark/internal/object"

// Logs holds the two state logs of one repository, opened together.
type Logs struct {
	Items *Log
	Discs *DiscLog
}

// TornTail is the torn tail that the open of one log found.
type TornTail struct {
	// Name is "state log" for the item log and "disc state log" for the
	// disc state log.
	Name  string
	Path  string
	Bytes int64
}

// OpenLogs reads and replays the item log and the disc state log in
// stateDir. holdsLock tells whether the command holds the repository
// lock. A holder of the lock opens both logs writable, and the open cuts
// a torn tail. A command without the lock opens both logs read-only: the
// open ignores a torn tail, changes no file, and every append fails.
// TornTails reports each tail for the warning.
func OpenLogs(stateDir string, holdsLock bool) (*Logs, error) {
	items, err := openLog(stateDir, holdsLock)
	if err != nil {
		return nil, err
	}
	discs, err := openDiscLog(stateDir, holdsLock)
	if err != nil {
		return nil, err
	}
	return &Logs{Items: items, Discs: discs}, nil
}

// TornTails returns one entry for each log whose open found a torn tail.
func (s *Logs) TornTails() []TornTail {
	var out []TornTail
	if n := s.Items.TornBytes(); n > 0 {
		out = append(out, TornTail{Name: "state log", Path: s.Items.Path(), Bytes: n})
	}
	if n := s.Discs.TornBytes(); n > 0 {
		out = append(out, TornTail{Name: "disc state log", Path: s.Discs.Path(), Bytes: n})
	}
	return out
}

// Word returns the derived word of the item id, and whether the item
// log knows id. It reads the disc of a Packed record from the disc state
// log.
func (s *Logs) Word(id object.ID) (ItemWord, bool) {
	rec, ok := s.Items.Get(id)
	if !ok {
		return "", false
	}
	disc, _ := s.Discs.Disc(rec.DiscUUID)
	return Word(rec, disc), true
}
