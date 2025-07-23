# Noah's Ark Backup System - Design Document

## Overview

Noah's Ark is a Git-like backup system specifically designed for Blu-ray optical media with Forward Error Correction (FEC) capabilities. The system provides distributed backup across multiple discs with content-addressable storage, automatic deduplication, and robust error recovery mechanisms.

## Core Features

### 1. Multi-Disc Support
- **Flexible Disc Sizes**: Support for BD25 (25GB), BD50 (50GB), and BD100 (100GB) discs
- **Dynamic Capacity Management**: Automatically transitions between different disc sizes based on availability
- **Smart Space Allocation**: Reserves 5% of disc capacity for metadata and filesystem overhead

### 2. Content-Addressable Storage
- **SHA512 Hashing**: Uses 512-bit SHA for better collision resistance than SHA256
- **Object Model**: Git-like blob, tree, and commit objects
- **Deduplication**: Identical files stored only once across all backups

### 3. Forward Error Correction (FEC)
- **Reed-Solomon Erasure Coding**: Primary FEC method for data protection
- **Configurable Redundancy**: Support for 5%, 15%, and 30% redundancy levels
- **Cross-Disc Protection**: FEC stripes can span multiple discs
- **Automatic Recovery**: Can recover data from missing or corrupted discs

## Architecture

### Directory Structure
```
.noahsark/
├── objects/          # Content-addressable object storage (metadata)
│   └── xx/           # First 2 hex chars of SHA512
│       └── ...       # Remaining hash chars
├── storage/          # Actual disc content organized by disc ID
│   └── [DISC_ID]/    # Per-disc storage
│       ├── xx/       # Hash-based directory structure
│       └── parity/   # FEC parity data
├── commits/          # Commit metadata and history
├── discs/           # Disc usage and capacity tracking
├── fec/             # FEC stripe metadata
├── config           # System configuration
└── HEAD             # Current commit reference
```

### Core Components

#### 1. BackupSystem
Main orchestrator handling:
- Disc management and capacity tracking
- File chunking and distribution
- FEC stripe creation and management
- Commit and versioning operations

#### 2. FEC Engine Interface
Pluggable error correction system supporting:
- Reed-Solomon erasure coding
- PAR2-style recovery blocks
- Hamming codes for small errors
- Custom FEC implementations

#### 3. Storage Management
- **Blob Storage**: Files split into chunks across discs
- **Tree Storage**: Directory structure representation
- **Commit Storage**: Snapshot metadata and history
- **Parity Storage**: FEC redundancy data

## Data Structures

### Blob Object
```go
type Blob struct {
    Hash         string        // SHA512 of file content
    Size         int64         // Original file size
    Chunks       []BlobChunk   // Data chunks across discs
    FilePath     string        // Original file path
    FECProtected bool          // Whether FEC is enabled
    FECMethod    FECMethod     // Type of error correction
    ParityChunks []ParityChunk // Associated parity data
}
```

### BlobChunk
```go
type BlobChunk struct {
    DiscID      string  // Which disc contains this chunk
    StartByte   int64   // Start position in original file
    EndByte     int64   // End position in original file
    ChunkIndex  int     // Sequential chunk number
    Hash        string  // SHA512 of chunk data
    Compressed  bool    // Whether chunk is compressed
    FECStripeID string  // Associated FEC stripe
}
```

### FEC Stripe
```go
type FECStripe struct {
    ID           string        // Unique stripe identifier
    DataChunks   []BlobChunk   // Data chunks in stripe
    ParityChunks []ParityChunk // Parity chunks for recovery
    Method       FECMethod     // Error correction method
    CanRecover   func(int) bool // Recovery capability check
}
```

## Error Correction Strategy

### Reed-Solomon Implementation
- **Data Shards**: 10 data blocks per stripe (configurable)
- **Parity Shards**: 4 parity blocks per stripe (can lose up to 4 discs)
- **Shard Size**: 64MB per shard (configurable)
- **Recovery**: Can reconstruct any missing data from available shards

### Redundancy Levels
- **None (0%)**: No redundancy, maximum storage efficiency
- **Low (5%)**: Basic protection against single disc failure
- **Medium (15%)**: Protection against multiple disc failures
- **High (30%)**: Maximum protection for critical data

### Cross-Disc Protection
- Data and parity chunks distributed across different discs
- Ensures recovery even with complete disc loss
- Parity data preferentially stored on different disc types

## Backup Process Flow

### 1. File Analysis
```
File Input → SHA512 Calculation → Deduplication Check → Chunking Decision
```

### 2. Chunking Strategy
```
Large File → Fixed-Size Chunks → Hash Calculation → Disc Assignment
```

### 3. FEC Creation
```
Data Chunks → Stripe Formation → Parity Generation → Cross-Disc Distribution
```

### 4. Commit Creation
```
Tree Building → Commit Object → Metadata Storage → HEAD Update
```

## Recovery Process

### 1. Damage Assessment
- Scan available discs for readable chunks
- Identify missing or corrupted data
- Determine recovery feasibility using FEC metadata

### 2. FEC Recovery
- Group chunks by FEC stripe
- Apply Reed-Solomon decoding for missing chunks
- Verify recovered data using SHA512 hashes

### 3. File Reconstruction
- Reassemble chunks in correct order
- Verify complete file integrity
- Report any unrecoverable data

## Configuration Options

### BackupConfig Structure
```go
type BackupConfig struct {
    RootPath         string    // Backup source directory
    DiscPreferences  []string  // Preferred disc types in order
    FECMethod        FECMethod // Error correction method
    RedundancyLevel  float64   // 0.0 to 1.0 redundancy ratio
    VerifyWrites     bool      // Verify data after writing
    CompressionLevel int       // 0-9 compression level
    EncryptionKey    []byte    // Optional encryption key
    MaxChunkSize     int64     // Maximum chunk size
    CrossDiscFEC     bool      // Enable cross-disc FEC
}
```

### Disc Configuration
```go
type DiscConfig struct {
    Type     string  // "BD25", "BD50", "BD100"
    Capacity int64   // Bytes capacity
    Label    string  // Human-readable label
}
```

## Disaster Recovery Scenarios

### Scenario 1: Single Disc Loss
- **Impact**: Loss of chunks stored on failed disc
- **Recovery**: Use FEC parity data from other discs
- **Time**: Fast recovery using Reed-Solomon decoding

### Scenario 2: Multiple Disc Loss
- **Impact**: Loss of multiple chunks from same FEC stripe
- **Recovery**: Possible if lost discs ≤ parity shards count
- **Time**: Moderate recovery time depending on data distribution

### Scenario 3: Metadata Loss
- **Impact**: Loss of .noahsark directory
- **Recovery**: Reconstruct from metadata copies on discs
- **Time**: Extended recovery requiring disc scanning

### Scenario 4: Catastrophic Loss
- **Impact**: Loss of majority of discs
- **Recovery**: Partial recovery of surviving complete stripes
- **Time**: Manual recovery process required

## Performance Considerations

### Write Performance
- **Parallel Processing**: Concurrent chunk processing and hashing
- **Streaming**: Large file streaming to avoid memory exhaustion
- **Batch Operations**: Group disc operations for efficiency

### Read Performance
- **Cache Strategy**: Keep frequently accessed metadata in memory
- **Predictive Loading**: Pre-load likely needed chunks
- **Parallel Recovery**: Concurrent FEC decoding across stripes

### Storage Efficiency
- **Compression**: Optional per-chunk compression
- **Deduplication**: Content-addressable storage eliminates duplicates
- **Smart Allocation**: Optimal chunk distribution across discs

## Security Features

### Data Integrity
- **SHA512 Verification**: End-to-end data integrity checking
- **Chunk-Level Hashing**: Individual chunk verification
- **Commit Signing**: Optional cryptographic commit signatures

### Encryption Support
- **Transparent Encryption**: Optional AES encryption before storage
- **Key Management**: Secure key derivation and storage
- **Per-File Encryption**: Different keys for different files

## Implementation Phases

### Phase 1: Core System
- [ ] Basic blob, tree, commit objects
- [ ] Simple disc management
- [ ] SHA512 hashing and deduplication
- [ ] Basic chunking without FEC

### Phase 2: FEC Integration
- [ ] Reed-Solomon implementation
- [ ] FEC stripe management
- [ ] Cross-disc parity distribution
- [ ] Recovery algorithms

### Phase 3: Advanced Features
- [ ] Compression support
- [ ] Encryption integration
- [ ] Performance optimization
- [ ] Concurrent operations

### Phase 4: Production Features
- [ ] CLI interface
- [ ] Disc burning integration
- [ ] Progress reporting
- [ ] Verification tools

## Testing Strategy

### Unit Tests
- Hash calculation verification
- FEC encoding/decoding correctness
- Chunk splitting and reconstruction
- Object serialization/deserialization

### Integration Tests
- Multi-disc backup scenarios
- Recovery from various failure modes
- Performance benchmarking
- Stress testing with large datasets

### Disaster Recovery Tests
- Simulated disc failures
- Metadata corruption scenarios
- Partial recovery validation
- Cross-platform compatibility

## Dependencies

### Core Dependencies
- **Go Standard Library**: File I/O, cryptography, compression
- **Reed-Solomon Library**: `github.com/klauspost/reedsolomon`
- **Command Line Interface**: `github.com/spf13/cobra`
- **Progress Bars**: `github.com/schollz/progressbar/v3`

### Platform-Specific Dependencies
- **Windows**: UDF formatting via `format` command
- **Linux**: Disc burning via `growisofs` and `cdrecord`
- **macOS**: Disc Utility integration

## Future Enhancements

### Advanced FEC Methods
- **LDPC Codes**: Low-Density Parity-Check codes for better efficiency
- **Raptor Codes**: Fountain codes for streaming applications
- **Adaptive FEC**: Dynamic redundancy based on disc reliability

### Cloud Integration
- **Hybrid Storage**: Combine optical and cloud storage
- **Remote Parity**: Store parity data in cloud for local data
- **Synchronization**: Sync metadata across multiple locations

### Performance Improvements
- **GPU Acceleration**: Hardware-accelerated FEC calculations
- **Network Distribution**: Distribute computation across multiple machines
- **Advanced Compression**: Context-aware compression algorithms

## Conclusion

The Noah's Ark backup system provides a robust, scalable solution for long-term data archival using optical media. The combination of Git-like versioning, content-addressable storage, and advanced error correction creates a reliable backup system capable of surviving multiple disc failures while maintaining data integrity over extended periods.

The modular design allows for future enhancements and adaptations to different storage media while maintaining backward compatibility with existing backups.
