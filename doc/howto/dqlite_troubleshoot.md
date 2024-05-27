# How to troubleshoot (some) Dqlite errors

Dqlite is the distributed database that LXD uses to store information
that must be synchronized across a cluster.
See {ref}`database` for more information.

This how-to guide describes strategies for how to respond to Dqlite-related
errors.

## Recognizing Dqlite-related errors

If LXD fails to start up or crashes, you should suspect a Dqlite-related error
if the error message mentions keywords like `Dqlite`, `raft`, or `segment`.

A known risk factor for some of the errors covered below is a previous LXD
crash caused by running out of disk space.

## The Dqlite data directory

When investigating Dqlite-related errors, it's essential to look at the
contents of the {ref}`Dqlite data directory <database-location>` for the affected node. This is the
directory where the local instance of Dqlite stores all its data.
You can find this directory at `/var/snap/lxd/common/lxd/database/global` (if you use
the snap) or `/var/lib/lxd/database/global` (otherwise).

The data directory contains several types of file. The most important types are:

- Closed segments: These have filenames like
  `0000000000056436-0000000000056501`. The two numbers are the *start index*
   and *end index*. Both indices are inclusive.
- Open segments: These have filenames like `open-1`.
- Snapshot files: These have names like `snapshot-1-59392-27900`. The first
  number is the *snapshot index*.
- Snapshot metadata files: These have names like `snapshot-1-59392-27900.meta`
  and are paired with snapshot files.

## Spotting anomalies

When looking at the contents of the data directory, watch for the following
symptoms:

1. Closed segments whose index ranges overlap (remember that these ranges are
   inclusive).
1. A closed segment with end index X where the next closed segment has start
   index greater than X + 1.
1. A snapshot file with snapshot index X where the next closed segment has
   start index greater than X + 1.
1. A snapshot file whose size is less than the size of a previous
   (lower-numbered) snapshot.

When scanning for these symptoms, start with the most recent snapshots and
closed segments (those with the highest indices) since the problem is more
likely to be there.

## Specific error messages

- `closed segment [...] is past last snapshot [...]`: This indicates that you
   have symptom 3 above (missing entries after a snapshot), possibly combined
   with symptom 1 (overlapping segments).
- `load closed segment [...]: entries count in preamble is zero`: This
   indicates that the mentioned segment is corrupt.

## Interventions

```{important}
Before taking any of the actions below, back up the entire
Dqlite data directory, so you don't lose data in case something goes wrong.
```

Here are some actions you can take in response to specific Dqlite errors. They
are not guaranteed to work in any specific case.

- If you have overlapping closed segments (symptom 1), try deleting some of
  them to remove the overlap, without creating gaps in the sequence of indices
  or removing any index that was previously represented.
- If the snapshot file with the highest index is unexpectedly small (symptom
  4), and there are still closed segments covering all the indices up to and
  including this snapshot's index, delete the snapshot and its corresponding
  metadata file.
- If the last (highest-numbered) closed segment is corrupt, try deleting it.
  (Deleting closed segments before the last one will create a gap and generally
  prevent Dqlite from starting.)

## Get help

If the tips above don't help with your situation, you can always post on the
LXD support forum. Make sure to mention Dqlite in your post and include the error
message or messages you're seeing, LXD logs, and the output of the following command (if
you're using the LXD snap):

```
sudo ls -lah /var/snap/lxd/common/lxd/database/global
```

Also mention any troubleshooting steps you've already taken and what
you learned.
