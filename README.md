# memetagfs

This is a hierarchic tag filesystem implemented in Go. The name implies it's designed to store memes but you can use it
for any files. The name was chosen so that it's unique enough to find in Google.

# Motivation

This project mostly replicates my older one called [jtagsfs](https://github.com/rkfg/jtagsfs). I removed some unused features
and added some that I needed, also the codebase is much more clean now. This version uses less RAM and is more portable, doesn't
have runtime dependencies so I actually run it on my SOHO router from a USB stick. I optimize it to perform good enough on a
580 MHz/128 Mb MIPS system so it should be blazing fast on a typical desktop PC.

# Platforms
## Supported OS

Linux only for now. There's no platform-specific code in the filesystem itself but it uses [the FUSE library](https://github.com/bazil/fuse)
that currently only supports Linux and BSD and I don't use BSD. Tell me if it works there. Windows support is usually tricky for
custom filesystems and requires 3rd party driver installation and whatnot. 

## Supported architectures

- x86_64
- MIPS
- i686 (not tested but should be okay)
- ARM (not tested but should be okay)

# Installation

Get a prebuilt binary from the [releases tab](https://github.com/rkfg/memetagfs/releases) or install a Go toolchain and do `go get github.com/rkfg/memetagfs`
The ready to launch binary should appear in `~/go/bin/memetagfs`. You can copy it wherever you like. If you crosscompile for other
architectures make sure you have a crosscompiler which is required to build the SQLite library (automatically). On Debian you can
install `gcc-arm-linux-gnueabi` package for ARM and `gcc-multilib-mipsel-linux-gnu` for MIPS. You'll need a regular GCC environment
anyway to build for x86.

# Usage

## Big fat warning

Due to the way filesystems work there's a thing you should know beforehand. **NEVER** delete directories under the `browse` directory.
Any file managers and commands I ever heard do that recursively which means they will visit all possible combinations of tags removing
all files they'll find effectively wiping most or all of your files! It's not possible to delete the directories themselves in `browse`
but files can and will be deleted. If you need to delete a tag do it only in the `tags` directory which is made exactly for managing
tags. If you need to change the tags a file belongs to, move it to another combination of tags. Deleting a file physically deletes it
from the storage, forever! Not just from this particular combination of tags.

## Creating tags

First, you need to create an empty directory for the database and storage.
Create another empty directory where your filesystem should be mounted to. Then launch
`memetagfs -s /path/to/storage -d /path/to/database.db /path/to/mountpoint`
to mount it. You'll have 2 directories inside, `browse` and `tags`. Create your tags inside the `tags` directory (as directories).
If you want some tags only be visible inside other tags, create them accordingly. For example, you may have tags
like `HD`, `BD` or `DVD` that are only applicable for the `video` tag and there's no reason to have them
visible in `pictures` tag. You then create the `video` tag, cd to it and create those `HD`, `BD` or `DVD`
tags inside.

It's also possible to _include_ a tag subtree to another tag when two distinct tags share the same children. Suppose you have
`pics` and `vids` tags for pictures and videos (memes, for example). Most of the child tags are the same for both categories.
One solution could be just putting all those tags at the same root level so they're visible in both and if it works for you, great!
But what if you also have completely different root tags like `music` or `books`? You certainly don't want to see `cats` or `pepe`
among the music-related tags. This was not possible to solve reliably in `jtagsfs` but it is in `memetagfs`! It supports a special
**tag group** tag type that can be included in other regular tags. Prepend the tag name with `!` and it becomes a tag group.
Tag groups are not visible by themselves. Create some tags you want to share between other tags under a tag group, for example, `!memes`. Let's say you've created `cats`, `dogs` and `frogs` inside `!memes`. Now you should include this group to both `pics` and `vids`. For that simply rename the directories adding the tag group name (without `!`) between pipes like this: `pics |memes|` and
`vids |memes|`. Now when you open these tags (see [Browsing](#Browsing)) you'll see not only the direct children you might have added
to them (specific to `pics` and `vids`) but also everything you've added to `!memes`. You can include more than one tag group
separating them with commas. For example, you might want to create a `!quality` group with `high`, `medium` and `low` tags representing
the perceived quality of content. But that can also be used for `music`! So then you rename the root tags as this: 
`vids |memes, quality|` (space after comma is optional) and `music |quality|`.

## Browsing

The `browse` directory is where you put your files to and where you query them.
For example, you want to tag a movie with tags `video`, `HD`, `thriller` and `sci-fi`. You cd to 
`/path/to/mountpoint/browse/video/HD/thriller/sci-fi/@` (the order isn't important) and copy your file there.
The `@` directory marks the end of the tags set.

By default tags are concatenated with boolean AND so when you search for a file you specify a set of tags that it should
have. You may also exclude some of the tags with `_`. For example, you want to find all pictures of cats but without dogs (because some
pictures might have both and you tagged them with both tags). For that you go to `/path/to/mountpoint/browse/pics/cats/_/dogs/@` and see
the result of the query. Most of the time the default behavior will be just what you need.

To change tags just move the file to another tag path. If you remove the file, it's removed from the storage forever,
not just from this tag or set of tags! If you want to remove one or several tags, move the file to the path that doesn't
contain these tags. If the same file is visible there you can drop it to the `@@` directory instead so there's no conflict.

There's also a special `@@` directory which shows internal file IDs and all tags the file has. It's useful to check
if a file is tagged correctly. You may move files from and to this directory just as fine as to/from `@`. Don't try
to change tags by renaming files here, the only part that matters is the actual filename. Tags are shown just for
information and changing them would lead to renaming a file to itself.

You may move, rename and delete tags in the `tags` directory. If a tag has files on it you won't be able to delete such tag.

## Subdirectories

Another feature that `jtagsfs` lacks is subdirectories inside query results (`@`). Sometimes you want to group multiple files together
but don't want to create a new tag just for that. For instance, you might have photos from a trip that fit several tags but you also
want to organize them by location or year that don't need to be tags. You can simply create directories inside `@` and put your files
there. It just works, the directory itself gets the assigned tags. It can contain files and other directories, there's no limit.

## Duplicates

Due to the nature of semantic filesystems sometimes you can get more than one file with the same name in the query results. Consider the
following example: file 1.jpg that belongs to tags `pics`, `cats` and another file 1.jpg that belongs to `pics` and `dogs`. It's perfectly valid but what would happen if you visit just `pics` tag? Both files would appear and without special measures there's no way
for other programs to differentiate between these two. Such situation is handled by prefixing the filenames with the IDs of the database
records. So in this case the files would look like `1231|1.jpg` and `389|1.jpg` (if their internal IDs are 1231 and 389). If you rename
either of the files, the deduplication mechanic will turn off and you'll see the original filenames again. Moving such files around
is fine, the deduplicating prefix is transparently removed. As a consequence, you can't use the `|` symbol in the filenames.

# Checking for errors

Software has bugs. It's inevitable. But losing data because of that is unacceptable (even though it happens sometimes). Memetagfs
can check the database and storage for contradictions and salvage the files that for some reason lost all database records and became
invisible. Use `--fsck` parameter to scan the internals and report the number of errors found. Use an additional `-f` flag to fix those
errors. All unreferenced files will be put to a root tag `lost+found` (it will be created if it doesn't exist) and you can sort them
later. Any database records that refer to non-existing files will be deleted.