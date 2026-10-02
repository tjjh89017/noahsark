package catalog

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/tjjh89017/noahsark/internal/format"
	"github.com/tjjh89017/noahsark/internal/object"
)

// objectHeadLen is the length of the common header and the object
// header at the start of each object file.
const objectHeadLen = format.CommonHeaderLen + format.ObjectHeaderLen

// errObjectLength reports an object file whose length is not the length
// that its object header gives.
var errObjectLength = errors.New("the file length does not match its object header")

// checkObject checks that raw is one whole object file of kind kind
// whose content id is id: the header CRC, the kind, the file length, and
// the content id of the decompressed payload.
func checkObject(kind format.ObjectKind, id object.ID, raw []byte) error {
	if len(raw) < objectHeadLen {
		return errObjectLength
	}
	_, oh, err := format.DecodeObjectFileHeader(raw[:objectHeadLen])
	if err != nil {
		return err
	}
	if oh.Kind != kind {
		return fmt.Errorf("the object header gives kind %d, want %d", oh.Kind, kind)
	}
	if uint64(len(raw)-objectHeadLen) != oh.StoredLen {
		return errObjectLength
	}
	payload, err := object.Decompress(raw[objectHeadLen:], oh.Compression, oh.PayloadLen)
	if err != nil {
		return err
	}
	if object.ComputeID(kind, payload) != id {
		return errors.New("the content id does not verify")
	}
	return nil
}

// checkObjectHead reads only the headers of the object file at path and
// checks the header CRC, the kind and the file length. It reports false
// for a missing, empty or short file. It reads 64 bytes, not the payload,
// thus it does not check the content id.
func checkObjectHead(kind format.ObjectKind, path string) (bool, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return false, err
	}
	head := make([]byte, objectHeadLen)
	if _, err := io.ReadFull(f, head); err != nil {
		return false, nil
	}
	_, oh, err := format.DecodeObjectFileHeader(head)
	if err != nil || oh.Kind != kind {
		return false, nil
	}
	return uint64(info.Size()-objectHeadLen) == oh.StoredLen, nil
}

// DamagedObjectError reports a catalog object file that does not give
// its content id. recover, or a counted verify, of the disc that holds
// the object writes a good copy.
type DamagedObjectError struct {
	Kind format.ObjectKind
	ID   object.ID
	Err  error
	// DiscUUID is the disc whose catalog INDEX lists the object.
	// HasDiscUUID is false when no INDEX of the catalog names a disc.
	DiscUUID    [16]byte
	HasDiscUUID bool
	Label       string
}

func (e *DamagedObjectError) Error() string {
	head := fmt.Sprintf("catalog: %s %s is damaged: %v; ", kindWord(e.Kind), e.ID.TextForm(), e.Err)
	if e.HasDiscUUID {
		return head + fmt.Sprintf("run recover with disc %s%s, the disc that holds it", uuidText(e.DiscUUID), LabelSuffix(e.Label))
	}
	return head + "run recover with the disc that holds it"
}

func (e *DamagedObjectError) Unwrap() error { return e.Err }

// kindWord names a metadata object kind in a message.
func kindWord(kind format.ObjectKind) string {
	switch kind {
	case format.ObjectKindSnapshot:
		return "snapshot"
	case format.ObjectKindTree:
		return "tree"
	case format.ObjectKindBlob:
		return "blob"
	}
	return fmt.Sprintf("object of kind %d", kind)
}

// damaged builds the DamagedObjectError of one object, with the disc
// that holds it when an INDEX of the catalog names one.
func (c *Catalog) damaged(kind format.ObjectKind, id object.ID, err error) *DamagedObjectError {
	e := &DamagedObjectError{Kind: kind, ID: id, Err: err}
	if loc, found := c.LocateObject(id); found {
		e.DiscUUID, e.HasDiscUUID = loc.DiscUUID, true
		if row, found := c.DiscRow(loc.DiscUUID); found {
			e.Label = discLabelText(row)
		}
	}
	return e
}

// readObject reads the object file of kind and id and checks it against
// id. A missing file gives the error of os.ReadFile, thus os.IsNotExist
// holds for it. A file that does not give id gives a
// *DamagedObjectError.
func (c *Catalog) readObject(kind format.ObjectKind, id object.ID) ([]byte, error) {
	raw, err := os.ReadFile(c.MetaPath(kind, id))
	if err != nil {
		return nil, err
	}
	if err := checkObject(kind, id, raw); err != nil {
		return nil, c.damaged(kind, id, err)
	}
	return raw, nil
}

// Holds reports whether the catalog holds a snapshot, tree or blob object
// with the content id id whose file gives that id. It reports false for a
// chunk: the catalog holds no chunk. A content id covers the kind, thus
// at most one kind can give id.
func (c *Catalog) Holds(id object.ID) bool {
	for _, kind := range []format.ObjectKind{format.ObjectKindSnapshot, format.ObjectKindTree, format.ObjectKindBlob} {
		raw, err := os.ReadFile(c.MetaPath(kind, id))
		if err == nil && checkObject(kind, id, raw) == nil {
			return true
		}
	}
	return false
}

// FileSizeError reports a tree entry whose size is not the sum of the
// chunk lengths of its blob.
type FileSizeError struct {
	Blob      object.ID
	EntrySize uint64
	BlobSize  uint64
	Overflow  bool
}

func (e *FileSizeError) Error() string {
	if e.Overflow {
		return fmt.Sprintf("catalog: blob %s: the chunk lengths overflow a file size", e.Blob.TextForm())
	}
	return fmt.Sprintf("catalog: blob %s: the chunk lengths give %d bytes, but the tree entry gives %d",
		e.Blob.TextForm(), e.BlobSize, e.EntrySize)
}

// ReadFileBlob reads the blob of the regular file entry e and checks
// that the lengths of its chunks add up to the size of e. A mismatch is
// a *FileSizeError.
func (c *Catalog) ReadFileBlob(e format.TreeEntry) (*format.Blob, error) {
	id := object.ID(e.ContentID)
	b, err := c.ReadBlob(id)
	if err != nil {
		return nil, err
	}
	var sum uint64
	for _, be := range b.Entries {
		if sum+be.Length < sum {
			return nil, &FileSizeError{Blob: id, EntrySize: e.Size, Overflow: true}
		}
		sum += be.Length
	}
	if sum != e.Size {
		return nil, &FileSizeError{Blob: id, EntrySize: e.Size, BlobSize: sum}
	}
	return b, nil
}
