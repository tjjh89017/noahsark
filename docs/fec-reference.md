# The rs255-gf8 forward error correction code: a reference for implementers

This document lets an independent implementer reproduce NoahsArk's parity
bytes without a Reed-Solomon library. FORMAT.md is the authority for the
on-disc format; read its Forward error correction, Objects, and Run index
sections for the byte layouts this document assumes. Numbers stay stable
across edits to those documents; find the current section by its heading
name, not by a number this document does not repeat.

NoahsArk itself does not run this code. It calls the
`github.com/klauspost/reedsolomon` library instead. See "Library
equivalence" at the end of this document.

## The field: GF(2^8)

Every arithmetic operation in this code works on one byte at a time, in
the finite field GF(2^8). The field has 256 elements, the byte values 0
through 255. Addition and subtraction are both the XOR operation.
Multiplication and division need the reduction polynomial and the
exponent and log tables below.

The reduction polynomial is:

```
polynomial = 0x11D   (x^8 + x^4 + x^3 + x^2 + 1)
```

Build the exponent table (`exp`) and the log table (`log`) once, at
startup. `exp[i]` holds `generator^i`; `log[x]` holds the `i` such that
`exp[i] == x`. The generator is `0x02`.

```
function buildTables():
    exp = array of 255 bytes
    log = array of 256 bytes
    x = 1
    for i in 0..254:
        exp[i] = x
        log[x] = i
        x = carrylessMultiply(x, generator, polynomial)
    return exp, log

function carrylessMultiply(a, b, polynomial):
    r = 0
    aa = a          // widened to at least 16 bits
    bb = b
    while bb != 0:
        if (bb & 1) != 0:
            r = r XOR aa
        aa = aa << 1
        if (aa & 0x100) != 0:
            aa = aa XOR polynomial
        bb = bb >> 1
    return r as a byte
```

`carrylessMultiply` is the field multiplication computed the slow way, by
polynomial multiplication over GF(2) followed by reduction modulo
`polynomial`. It only runs 255 times, while building the tables; every
other multiplication in this document uses the table-based `mul` below.

```
function mul(a, b):
    if a == 0 or b == 0:
        return 0
    sum = log[a] + log[b]
    if sum >= 255:
        sum = sum - 255
    return exp[sum]

function inv(a):
    // a must be nonzero; the field has no inverse for 0.
    diff = 255 - log[a]
    if diff == 255:
        diff = 0
    return exp[diff]

function div(a, b):
    // b must be nonzero.
    return mul(a, inv(b))
```

Known values, useful to check a from-scratch table build: `inv(1) =
0x01`, `inv(2) = 0x8E`, `inv(3) = 0xF4`, `inv(4) = 0x47`, `inv(5) =
0xA7`, `inv(6) = 0x7A`.

## The Cauchy matrix

The code has two parameters: `k`, the number of data shards (blocks) in
one stripe, and `m`, the number of parity shards. NoahsArk's version 1
geometry is `k = 231`, `m = 23`; `k + m` must never exceed 255, since
every row and column index is a single GF(2^8) element.

Build the `m` by `k` Cauchy matrix `C`:

```
function buildCauchyMatrix(k, m):
    C = matrix of m rows, k columns
    for j in 0..m-1:
        xj = k + j
        for i in 0..k-1:
            yi = i
            C[j][i] = inv(xj XOR yi)
    return C
```

`xj` and `yi` range over `0..k+m-1` without overlap (`xj` starts at `k`,
`yi` stops at `k-1`), so `xj XOR yi` is never zero and `inv` never fails
on this matrix by construction.

Stack `C` under the `k` by `k` identity matrix to get the full `(k+m)` by
`k` generator matrix `[I_k; C]`. Row `i` for `i < k` is the unit vector
with a 1 in column `i` and 0 elsewhere (an identity row); row `k+j` for
`j` in `0..m-1` is `C[j]`. Every data shard is stored as-is (its
generator row is a unit vector); every parity shard is a linear
combination of all `k` data shards, with coefficients from `C[j]`.

## Encoding one stripe

A stripe is `k` data blocks, `data[0..k-1]`, each the same length (2048
bytes on disc, `BlockSize` in FORMAT.md's Forward error correction
section). Encoding computes `m` parity blocks:

```
function encode(C, data, k, m, blockLen):
    parity = array of m blocks, each blockLen bytes, initialized to 0
    for j in 0..m-1:
        for i in 0..k-1:
            coeff = C[j][i]
            if coeff == 0:
                continue           // never happens for this matrix, but skip is free
            for t in 0..blockLen-1:
                parity[j][t] = parity[j][t] XOR mul(coeff, data[i][t])
    return parity
```

Every byte position `t` is computed independently; the same formula
runs `blockLen` times per row. This is `parity = C * data` in GF(2^8)
matrix notation.

## Decoding a stripe with erasures

A stripe has `k + m` shards in total, indexed `0..k-1` for data and
`k..k+m-1` for parity. Decoding needs any `k` of these `k + m` shards to
still be present (readable and uncorrupted); it recovers the rest.

```
function decode(C, k, m, shards):
    // shards: a map from shard index (0..k+m-1) to its bytes, for every
    // surviving shard. len(shards) must be at least k.
    indices = the keys of shards, sorted ascending, first k of them
    square = k by k matrix
    present = array of k blocks, the bytes for each chosen index
    for row, idx in indices (row = 0..k-1):
        present[row] = shards[idx]
        if idx < k:
            square[row] = unit vector with 1 at column idx
        else:
            square[row] = C[idx - k]

    inverse = invertMatrix(square)   // Gauss-Jordan, see below

    data = array of k blocks
    for i in 0..k-1:
        out = block of blockLen zero bytes
        for row in 0..k-1:
            coeff = inverse[i][row]
            if coeff == 0:
                continue
            for t in 0..blockLen-1:
                out[t] = out[t] XOR mul(coeff, present[row][t])
        data[i] = out

    parity = encode(C, data, k, m, blockLen)   // recompute all m parity blocks
    return data, parity
```

Any choice of `k` surviving rows out of the `k + m` rows of `[I_k; C]`
gives a square matrix that Gauss-Jordan elimination can invert (a
property of the Cauchy construction: every square submatrix of `[I_k;
C]` is non-singular). Recomputing `parity` from the recovered `data`
after decode gives every parity block too, even the erased ones, at the
cost of one more `encode` call.

### Inverting a square matrix over GF(2^8)

Gauss-Jordan elimination with row pivoting, using `mul`, `inv`, and XOR
in place of the usual real-number arithmetic:

```
function invertMatrix(m):
    // m: n by n matrix. Returns its inverse, or fails if singular.
    n = size of m
    aug = n by 2n matrix: left half is m, right half is the n by n identity

    for col in 0..n-1:
        pivot = the first row >= col with aug[row][col] != 0
        if no such row exists:
            fail: matrix is singular
        swap aug[col] and aug[pivot]

        inverse = inv(aug[col][col])
        for c in 0..2n-1:
            aug[col][c] = mul(aug[col][c], inverse)

        for row in 0..n-1, row != col:
            factor = aug[row][col]
            if factor == 0:
                continue
            for c in 0..2n-1:
                aug[row][c] = aug[row][c] XOR mul(factor, aug[col][c])

    return the right half of aug (columns n..2n-1)
```

## The stream mapping: files to blocks, columns and stripes

The k data columns of a run's stripes are not filled stripe by stripe;
they are filled by concatenating the run's data files, in the file
order FORMAT.md's Run index and catalog section defines, into one
logical stream of fixed-size blocks, then laying that stream out column
by column.

Padding rule: each file's bytes are padded with zero bytes up to a
whole `BlockSize` boundary before the next file begins. A file of `size`
bytes needs `ceil(size / BlockSize)` blocks; the last of those blocks
holds the file's final, possibly short, tail padded with zeros to
`BlockSize`.

```
function blockCountForFile(size, blockSize):
    return ceil(size / blockSize)

function buildStreamLayout(fileSizes, k, blockSize):
    fileStart = array, one entry per file: the block index it starts at
    total = 0
    for i, size in fileSizes:
        fileStart[i] = total
        total = total + blockCountForFile(size, blockSize)
    streamBlocks = total
    if streamBlocks > 0:
        stripeCount = ceil(streamBlocks / k)     // L, blocks in one column
    else:
        stripeCount = 0
    return fileStart, streamBlocks, stripeCount
```

Block `b` of the stream (`0 <= b < streamBlocks`) belongs to:

```
column = b / stripeCount      // integer division: which of the k data columns
stripe = b % stripeCount      // which row within that column
```

Partial last stripe: when `streamBlocks` is not a multiple of `k`, the
last stripe (`stripe == stripeCount - 1`) is missing blocks in the
highest-numbered columns. FORMAT.md's Forward error correction section
states how those missing positions are supplied as zero blocks for
encoding purposes; this document does not repeat that rule, since it is
a padding convention rather than field arithmetic.

## The checksum column

Alongside the `m` parity columns, the run keeps one checksum column: one
digest per data block, in data column order, per stripe.

```
function blockDigest(block):
    fullDigest = SHA256(block)
    return first 8 bytes of fullDigest

function buildChecksumRecord(stripeIndex, dataBlocks):
    digests = array, one entry per block in dataBlocks
    for i, block in dataBlocks:
        digests[i] = blockDigest(block)
    return record{stripeIndex, digestCount: length(dataBlocks), digests}

function verifyBlocks(record, dataBlocks):
    if length(dataBlocks) != length(record.digests):
        fail: digest count does not match block count
    bad = empty list
    for i, block in dataBlocks:
        if blockDigest(block) != record.digests[i]:
            append i to bad
    return bad     // indices of data blocks whose stored digest disagrees
```

A stripe with more bad data blocks than there are parity blocks (`m`)
cannot be repaired from this stripe's own parity; the checksum column is
what tells a reader which case it is in before it commits to a repair.

## Worked example: k=3, m=2

This example is small enough to check by hand. It is not a version 1
geometry (version 1 uses k=231, m=23); it exists only to check the
arithmetic above.

Cauchy matrix for k=3, m=2:

```
C[0] = F4 8E 01
C[1] = 47 A7 7A
```

Data: `data[0] = 0x53`, `data[1] = 0xA7`, `data[2] = 0x0C` (one byte per
shard, for a one-byte-long block).

Encoding:

```
p0 = mul(F4, 53) XOR mul(8E, A7) XOR mul(01, 0C)
   = 31 XOR DD XOR 0C
   = E0

p1 = mul(47, 53) XOR mul(A7, A7) XOR mul(7A, 0C)
```

The two products `mul(0xF4, 0x53) = 0x31` and `mul(0x8E, 0xA7) = 0xDD`
are worth checking directly against the exp/log tables built above.
Carrying the same arithmetic through row 1 gives:

```
p0 = 0xE0
p1 = 0xAD
```

Recovery example: suppose `data[0]` and `data[1]` are lost, and only
`data[2]`, `p0`, and `p1` survive. The chosen rows are index 2 (an
identity row, `[0, 0, 1]`), index 3 (row `C[0]`, `[F4, 8E, 01]`), and
index 4 (row `C[1]`, `[47, A7, 7A]`). Stack those three rows into a 3 by
3 matrix, invert it with `invertMatrix`, and multiply the inverse by the
column vector `[data[2], p0, p1]`; the result is `data[0] = 0x53`,
`data[1] = 0xA7`, `data[2] = 0x0C`, matching the original input.

## Library equivalence

NoahsArk's implementation does not run the pseudocode above at runtime.
It calls `github.com/klauspost/reedsolomon`, built with
`reedsolomon.New(k, m, reedsolomon.WithCauchyMatrix())`. A cross-check
before adopting the library confirmed that this option reproduces the
parity and recovery bytes this document describes, byte for byte, for
k=231, m=23 over several hundred random stripes and for the k=3, m=2
worked example above (`p0 = 0xE0`, `p1 = 0xAD`); the library's default
matrix (a Vandermonde matrix), and its `WithPAR1Matrix()` option, do
neither, so an implementation must pass `WithCauchyMatrix()` and never
change it.
