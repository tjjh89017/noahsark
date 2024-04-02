# Noah's Ark
Git-like based Backup Tool with Blu-Ray

## Design

### .noahsark folder

As git project, you will have a `.git` folder to store all git related info. Noahsark will have `.noahsark` in your folder.

### .noahsark/objects/*

`objects` folder will store all objects here. If SHA256's hex digest is `f5a7d6ac938ff6f5704e1e4abb4dd25f70086143c38bb17d59badec7f57b579c`, it will store in folder `f5` with the name `a7d6ac938ff6f5704e1e4abb4dd25f70086143c38bb17d59badec7f57b579c`

Content of the objects will be `blob`, `tree`, `commit`

- blob name will be the SHA256 hex digest in lowercase of the file.
- tree name will be the SHA256 hex digest in lowercase of the content of tree.
- commit name will be the SHA256 hex digest in lowercase of the content of commit.

#### Blob format

Considering file will larger than the remain space of the disc, this blob will be splitted into several pieces. Concatenate all file with the same SHA256 filename to get the full file. 

```
blob<LF>
<disc 1 ID><LF>
<disc 2 ID><LF>
```

#### Tree format

Tree is folder.

```
tree<LF>
<type><SP><SHA256 of tree or blob><SP><FILENAME><LF>
<type><SP><SHA256 of tree or blob><SP><FILENAME><LF>
<type><SP><SHA256 of tree or blob><SP><FILENAME><LF>
<type><SP><SHA256 of tree or blob><SP><FILENAME><LF>
```

Example:
```
tree
tree 6c3b21ffd100bcbad48b8f5fb93e5fad056308bd75f23fd837661c94e7b7de05 b
blob 6c3b21ffd100bcbad48b8f5fb93e5fad056308bd75f23fd837661c94e7b7de05 a
tree 71d8cfc933ea2b4a724869320f469909bd5ef1e3d2d1f3e2ec0bb17fb8c7234d aaa
blob f27e01fe7624cca3e69811a0bf9a4efd9dca9fd39f7a3a8f939cae0cfe8cdfb8 pic
```

#### Commit format

Commit will contain parent commit and timestamp. If this is first commit, parent's SHA256 will be all zero.

```
commit<LF>
parent<SP><64bytes SHA256 HEX><LF>
date<SP>2024-04-02 21:59:15.219620
tree<SP><tree sha256 64bytes hex>
---<LF>
<increase file A SHA256><SP><relpath of the file>
<increase file A SHA256><SP><relpath of the file>
<increase file A SHA256><SP><relpath of the file>
<increase file A SHA256><SP><relpath of the file>
<increase file A SHA256><SP><relpath of the file>
```

Example:
```
commit
parent 0000000000000000000000000000000000000000000000000000000000000000
date 2024-04-02 21:59:15.219620
tree b9bd46afd1c07773b861ab6e61c43acb3577e92f0ec4a568ac70fe92fa15510d
---
f27e01fe7624cca3e69811a0bf9a4efd9dca9fd39f7a3a8f939cae0cfe8cdfb8 ./b/a/a
70552fdbcb7e1fadb6f32a0324f265f8d592c5468c110b256ad86ca271a79bdf ./b/a
6c3b21ffd100bcbad48b8f5fb93e5fad056308bd75f23fd837661c94e7b7de05 ./b
1ca3609dada191e5244111e8a98de736d80e6a77586a23708ab0c59b0e9d3161 ./aaa/bbb/ccc/ddd/eee
97ee958e3c8827ad143fee6b307170b31b4568a4194b8073c47bb1227534a274 ./aaa/bbb/ccc/ddd
ed62f8e2d9a445a343eacf9f93c6ba3976db5c2dbb49215ca65422c616a065bc ./aaa/bbb/ccc
e9cf9990e2028d340ee0135bf385be011e181153293120ec55c806118d38646a ./aaa/bbb
71d8cfc933ea2b4a724869320f469909bd5ef1e3d2d1f3e2ec0bb17fb8c7234d ./aaa
3c1ace9dd406c4505a1507b27d50b348a62b852c9157c8bc07541a7a3cdebd3b ./btraw/a1/b1
6735fe9df35fb09ccc32cd472fc5a89bb49109e999c66f8679b2a9df15932d0e ./btraw/a1
3042ddc0f9ff5dedc0b5bd8ceecb7385c2b829b8d6b60eea03446c1981d263cd ./btraw/00-pseudo-img-note.txt
13010a37a251b615e15439bf6a4df2e2509ec2db2fcf479d634c91f76429548d ./btraw/sda3.torrent
b5c51fd8f88fe65dd9823c1d105112cdae34de83c7dcff0a08b480a9adf89caf ./btraw/sda1.torrent.info
e2875c467853d556d43f087a267fa4d30ee7c9903402332385fc803c27542d37 ./btraw/sda1.torrent
83fa3dc00e56a98a760cfc21ae9d37dc1a93d686fb8bb4ec35d60a22472cbc85 ./btraw/sda2.torrent
57ef94eee8d0373a4a37e8e2dd8c115acfc154d87bbb93a519a89e093d43d9e9 ./btraw/sda2.torrent.info
0eb3c868c5aa70858d018900a2b3f094ebe45d4c46a0a8a2be9d995535310a38 ./btraw/sda3.torrent.info
```

### .noahsark/disc/*

Store each disc ID and content that store in this disc. Name is disc ID. Content is the Blob and range.

```
<SHA256 of blob><SP><START offset><SP><END offset in bytes>
<SHA256 of blob><SP><START offset><SP><END offset in bytes>
<SHA256 of blob><SP><START offset><SP><END offset in bytes>
<SHA256 of blob><SP><START offset><SP><END offset in bytes>
```

### .noahsark/dump/*

Store the dumped commit info here. Every commit file will contain the disc ID that stored the new files. (Only new files in this commit). Name is commit id. Content is the disc ID.

```
<DISC ID 1>
<DISC ID 2>
<DISC ID 3>
<DISC ID 4>
```

### In Disc

In Disc, we will only have blob with SHA256 name and the content range.

If Disc should contain the data below.
```
f27e01fe7624cca3e69811a0bf9a4efd9dca9fd39f7a3a8f939cae0cfe8cdfb8 0 1023
71d8cfc933ea2b4a724869320f469909bd5ef1e3d2d1f3e2ec0bb17fb8c7234d 1024 255734908
```

You need to create `f2` and `71` folder, and have filename `7e01fe7624cca3e69811a0bf9a4efd9dca9fd39f7a3a8f939cae0cfe8cdfb8` in f2 and so on.

NOTE: If you need, you could copy the whole `.noahsark` folder into this disc to store the latest index folder

## Old design

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

- .noahsark/storage/xx
	- put the all disc file
	- if need, we could use this and burn again.
	- deleted files will be gone forever if your disc is missing.

- we don't consider if there is some gap between commit.py and dump.py
	- if you modify it, we don't care about it.
