# autosnap branch create

## autosnap branch create

Create and check out a Git branch with checkpoint refs

### Synopsis

Create and check out a Git branch, then copy autosnap checkpoint refs
from the previously checked-out branch into the new branch namespace.

This is equivalent to git checkout -b <branch> plus copying refs from
refs/autosnapshots/<source>/ to refs/autosnapshots/<branch>/.

```
autosnap branch create <branch> [flags]
```

### Examples

```
autosnap branch create feature/next
autosnap branch create feature/next --no-copy-checkpoints
autosnap branch create feature/next --overwrite
```

### Options

```
  -h, --help                  help for create
      --no-copy-checkpoints   Create the Git branch without copying checkpoint refs
      --overwrite             Replace colliding target checkpoint refs
```

### SEE ALSO

* [autosnap branch](autosnap_branch.md)	 - Create Git branches with autosnap checkpoints

