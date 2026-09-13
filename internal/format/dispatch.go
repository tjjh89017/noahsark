package format

// Dispatch reads the CommonHeader at the start of buf, checks the project
// magic and version_major, and calls the Decode method of the structure
// named by magic_kind. It returns the decoded structure and the number of
// bytes its Decode consumed.
//
// Dispatch is a scan-time helper: a caller that already knows a
// structure's Go type should call that type's own Decode directly.
func Dispatch(buf []byte) (any, int, error) {
	var h CommonHeader
	if err := h.Decode(buf); err != nil {
		return nil, 0, err
	}

	switch h.MagicKind {
	case MagicChunk:
		var v Chunk
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicBlob:
		var v Blob
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicTree:
		var v Tree
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicSnapshot:
		var v Snapshot
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicDisc:
		var v Disc
		if err := v.Decode(buf); err != nil {
			return nil, 0, err
		}
		return &v, DiscLen, nil
	case MagicRun:
		var v Run
		if err := v.Decode(buf); err != nil {
			return nil, 0, err
		}
		return &v, RunLen, nil
	case MagicIndex:
		var v Index
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicRefs:
		var v RefsTable
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	case MagicDiscs:
		var v DiscsTable
		n, err := v.Decode(buf)
		if err != nil {
			return nil, 0, err
		}
		return &v, n, nil
	default:
		return nil, 0, ErrBadMagic
	}
}
