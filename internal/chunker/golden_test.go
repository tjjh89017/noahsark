package chunker

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// updateGolden rewrites testdata/cutpoints_golden.txt from the chunker.
// The format freezes the cut points. Run `go test ./internal/chunker
// -update` only to create the file. Never run it to make a failing test pass.
var updateGolden = flag.Bool("update", false, "rewrite the cut point golden file in testdata")

const cutPointGoldenPath = "testdata/cutpoints_golden.txt"

// Frozen chunker parameters of format major 1.
const (
	frozenMin = 1 << 20
	frozenAvg = 4 << 20
	frozenMax = 16 << 20
)

// cutPointGoldenInput builds the golden input: random bytes, then an
// all-zero region, then a short random tail. The random part gives cuts
// between min and max. The zero region gives cuts of exactly max. The last
// cut in the random part lies randomTail bytes before its end, so the zero
// region ends exactly on a max cut. The tail is then a last chunk shorter
// than min.
func cutPointGoldenInput() []byte {
	const (
		randomLen  = 40 << 20
		randomTail = 2576377
		zeroLen    = 3*frozenMax - randomTail
		tailLen    = 300 << 10
	)
	data := make([]byte, randomLen+zeroLen+tailLen)
	rng := rand.New(rand.NewSource(20261008))
	rng.Read(data[:randomLen])
	rng.Read(data[randomLen+zeroLen:])
	return data
}

// goldenChunk is one line of the golden file.
type goldenChunk struct {
	offset int
	length int
	sum    string
}

func describeChunks(chunks [][]byte) []goldenChunk {
	var out []goldenChunk
	offset := 0
	for _, chunk := range chunks {
		sum := sha256.Sum256(chunk)
		out = append(out, goldenChunk{offset: offset, length: len(chunk), sum: hex.EncodeToString(sum[:])})
		offset += len(chunk)
	}
	return out
}

func goldenHeader() string {
	return fmt.Sprintf("# min %d avg %d max %d\n", frozenMin, frozenAvg, frozenMax)
}

func formatGolden(chunks []goldenChunk) []byte {
	var b bytes.Buffer
	b.WriteString(goldenHeader())
	b.WriteString("# offset length sha256\n")
	for _, c := range chunks {
		fmt.Fprintf(&b, "%d %d %s\n", c.offset, c.length, c.sum)
	}
	return b.Bytes()
}

func parseGolden(t *testing.T, raw []byte) []goldenChunk {
	t.Helper()
	var out []goldenChunk
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		var c goldenChunk
		if _, err := fmt.Sscanf(line, "%d %d %s", &c.offset, &c.length, &c.sum); err != nil {
			t.Fatalf("golden line %q: %v", line, err)
		}
		out = append(out, c)
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestCutPointGolden chunks a fixed input with the production profile at
// each read step of TestDeterminism. It compares the cut points and the
// chunk hashes with the checked-in golden file.
func TestCutPointGolden(t *testing.T) {
	if DefaultProfile.Min != frozenMin || DefaultProfile.Avg != frozenAvg || DefaultProfile.Max != frozenMax {
		t.Fatalf("DefaultProfile min/avg/max = %d/%d/%d, want %d/%d/%d",
			DefaultProfile.Min, DefaultProfile.Avg, DefaultProfile.Max, frozenMin, frozenAvg, frozenMax)
	}
	data := cutPointGoldenInput()

	if *updateGolden {
		chunks := describeChunks(chunkAll(t, bytes.NewReader(data), DefaultProfile))
		if err := os.MkdirAll(filepath.Dir(cutPointGoldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(cutPointGoldenPath, formatGolden(chunks), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(cutPointGoldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(raw, []byte(goldenHeader())) {
		t.Fatalf("golden header does not name min %d avg %d max %d", frozenMin, frozenAvg, frozenMax)
	}
	want := parseGolden(t, raw)
	checkGoldenCoversLimits(t, want, len(data))

	for _, step := range []int{1, 3, 7, 64, 4096} {
		got := describeChunks(chunkAll(t, &sizedReader{data: data, step: step}, DefaultProfile))
		if len(got) != len(want) {
			t.Fatalf("step %d: got %d chunks, want %d", step, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("step %d: chunk %d = %+v, want %+v", step, i, got[i], want[i])
			}
		}
	}
}

// checkGoldenCoversLimits checks that the golden file holds each kind of
// cut: a cut between min and max, a cut at exactly max, and a last chunk
// shorter than min. It also checks that no other chunk breaks the limits.
func checkGoldenCoversLimits(t *testing.T, chunks []goldenChunk, total int) {
	t.Helper()
	if len(chunks) == 0 {
		t.Fatal("golden file holds no chunks")
	}
	var sawMid, sawMax bool
	sum := 0
	for i, c := range chunks {
		if c.offset != sum {
			t.Fatalf("chunk %d offset %d, want %d", i, c.offset, sum)
		}
		sum += c.length
		if i == len(chunks)-1 {
			if c.length >= frozenMin {
				t.Errorf("last chunk length %d, want less than min %d", c.length, frozenMin)
			}
			continue
		}
		switch {
		case c.length < frozenMin+1 || c.length > frozenMax:
			t.Errorf("chunk %d length %d is outside [%d, %d]", i, c.length, frozenMin+1, frozenMax)
		case c.length == frozenMax:
			sawMax = true
		default:
			sawMid = true
		}
	}
	if sum != total {
		t.Errorf("golden chunks cover %d bytes, want %d", sum, total)
	}
	if !sawMid {
		t.Error("golden file holds no cut between min and max")
	}
	if !sawMax {
		t.Error("golden file holds no cut at max")
	}
}
