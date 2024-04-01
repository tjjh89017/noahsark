#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import os
import sys
import datetime
import hashlib
import nutree

import pprint as pp

class NoahObject:
    def __init__(self, name, p):
        self.name = name
        self.path = p

    def _hash(self):
        return self.path

class NoahTree(NoahObject):
    def __init__(self, name, p):
        super().__init__(name, p)
        self.content = []

    def __repr__(self):
        return f"NoahTree<{self.name}, {self.path}>"

    def put(self, o):
        self.content.append(o)

    def format(self):
        s = "tree\n".encode('utf-8')
        for x in self.content:
            s += f"{x['h']} {x['name']}\n".encode('utf-8')
        return s

    def sha(self):
        sha256 = hashlib.sha256()
        sha256.update(self.format())
        return sha256.hexdigest()

    def write(self, f):
        f.write("tree\n".encode('utf-8'))
        for x in self.content:
            f.write(f"{x['h']} {x['name']}\n".encode('utf-8'))


class NoahBlob(NoahObject):
    def __init__(self, name, p):
        super().__init__(name, p)

    def __repr__(self):
        return f"NoahBlob<{self.name}, {self.path}>"

    def sha(self):
        BUFFER_SIZE = 65536
        sha256 = hashlib.sha256()
        with open(self.path, 'rb') as f:
            while True:
                data = f.read(BUFFER_SIZE)
                if not data:
                    break
                sha256.update(data)
        return sha256.hexdigest()

    def write(self, f):
        # TODO need to split the file and put it in the disc
        pass


def _calc_id(tree, data):
    if isinstance(data, NoahObject):
        return data._hash()
    return hash(data)

def get_noahsark_dir():
    cwd = os.getcwd()
    while True:
        #print(cwd)
        if '.noahsark' in next(os.walk(cwd), (None, [], None))[1]:
            return cwd

        cwd_new = os.path.dirname(cwd)
        if cwd == cwd_new:
            return ""
        cwd = cwd_new

    return ""


def main():
    cwd = get_noahsark_dir()
    noahsark_dir = os.path.join(cwd, '.noahsark')
    if not cwd:
        print(".noahsark not found")
        sys.exit(1)
    print(cwd)
    print("---")

    excludes = ['.noahsark']

    tree = nutree.Tree(".", calc_data_id=_calc_id)
    root = NoahTree(".", ".")
    root_node = tree.add(root)

    # scan all dir
    for dirpath, dirnames, filenames in os.walk(cwd):
        if dirpath.startswith(noahsark_dir):
            # skip
            continue
        #print(dirpath)
        relpath = os.path.normpath(os.path.relpath(dirpath, cwd))
        if not relpath.startswith('.'):
            relpath = os.path.join('.', relpath)
        #print(relpath)
        n = tree
        n = tree.find(NoahObject("", relpath))
        #if relpath != '':
        #    #n = n.find(relpath)
        #    n = n.find(NoahObject("", relpath))
        #    print(n)
        
        ds = [d for d in dirnames if d not in excludes]
        for d in ds:
            n.add(NoahTree(d, os.path.join(relpath, d)))

        for f in filenames:
            n.add(NoahBlob(f, os.path.join(relpath, f)))

    print("---")
    tree.print()
    print("---")

    increase_list = []

    # traversal
    for n in tree.iterator(method=nutree.IterMethod.POST_ORDER):
        x = n.data
        print(f"sha256 {x.path}")
        h = x.sha()

        path = os.path.join(noahsark_dir, 'objects', h[:2])
        if not os.path.exists(path):
            os.makedirs(path)

        path = os.path.join(path, h[2:])
        if not os.path.exists(path):
            with open(path, 'wb') as f:
                x.write(f)
            print(f"commit {h} {x.path}")
            increase_list.append(f"{h} {x.path}")
        else:
            print(f"exist {h} {x.path}")

        parents = n.get_parent_list()
        if len(parents) > 0:
            parents[-1].data.put({'h': h, 'name': x.name})

    # save the root to commit

    # if HEAD exists, load it
    HEAD = '0' * 64
    head_path = os.path.join(noahsark_dir, 'HEAD')
    if os.path.exists(head_path):
        with open(path, 'r') as f:
            HEAD = f.read(64)

    root_tree_sha = root.sha()
    d = datetime.datetime.now()

    commit_content = f'''commit
parent {HEAD}
date {d} 
tree {root_tree_sha}
---
''' + '\n'.join(increase_list) + '\n'
    sha256 = hashlib.sha256()
    sha256.update(commit_content.encode('utf-8'))
    commit_sha256 = sha256.hexdigest()

    # write commit to objects
    print(f"write to commit {commit_sha256}")
    commit_dir = os.path.join(noahsark_dir, 'objects', commit_sha256[:2])
    if not os.path.exists(commit_dir):
        os.makedirs(commit_dir)
    commit_path = os.path.join(commit_dir, commit_sha256[2:])
    with open(commit_path, 'wb') as f:
        f.write(commit_content.encode('utf-8'))
    
    # write to HEAD
    print("write to HEAD")
    with open(head_path, 'wb') as f:
        f.write(commit_sha256.encode('utf-8'))
        f.write('\n'.encode('utf-8'))


    pass


if __name__ == '__main__':
    main()
