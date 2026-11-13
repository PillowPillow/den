package instance

import (
	"context"
	"errors"
	"io/fs"

	"github.com/PillowPillow/den/internal/registry"
	"github.com/PillowPillow/den/internal/sbx"
)

// State is what den V2 knows of an instance: its record, and whether sbx lists its VM.
type State int

const (
	Absent   State = iota
	Live           // record and VM
	Recorded       // record, no VM
	Foreign        // VM, no record
)

// Lookup returns an unreadable record as an error, whatever sbx lists: taking
// it for absent would let `den up` create over, or `den rm` skip, what a newer
// den recorded.
func Lookup(ctx context.Context, d Deps, instance string) (State, *registry.Record, error) {
	var rec *registry.Record
	r, err := registry.Read(d.DenHome, instance)
	switch {
	case err == nil:
		rec = &r
	case !errors.Is(err, fs.ErrNotExist):
		return Absent, nil, err
	}
	boxes, err := sbx.Ls(ctx, d.Sbx)
	if err != nil {
		return Absent, nil, err
	}
	listed := sbx.Find(boxes, instance) != nil
	switch {
	case rec != nil && listed:
		return Live, rec, nil
	case rec != nil:
		return Recorded, rec, nil
	case listed:
		return Foreign, nil, nil
	}
	return Absent, nil, nil
}
