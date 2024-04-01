# Noah's Ark
Git-like based Backup Tool with Blu-Ray

## Design

- git object like blob/tree/commit/HEAD
- target storage is blu-ray
- need to split the file if the blu-ray is full
- consider to use sha256 as blob filename
- commit and tree file need a copy in index folder
- need to support symlink (otherwise it will loop forever)

- index folder
	- should store all blob/tree/commit
	- blob should store the blu-ray id and user-defined name (optional)
	- blu-ray label or id should be the id

- blob
	- no compress
	- no blob header
- tree
	- no compress
	- no permission field
	- only in index folder
- commit
	- no compress
	- only in index folder
- link
	- no compress
	- point to the blu-ray
	- only in index folder

- blu-ray
	- UDF 2.50
	- save sha256 name file
	- use first byte to be the folder (first 2 hex)

- format UDF
	- in windows
	- `format /fs:UDF /V:<label> /Q /R:2.50 G:`

- linux
	- https://wiki.gentoo.org/wiki/CD/DVD/BD_writing
	- `growisofs -speed=X -Z /dev/sr0=test.udf`

- dump.py
	- need to write to objects and save which disc
	- need to write some metadata with disc name and the content of the disc (from start to end)
