# autosnap branch

## autosnap branch

Create Git branches with autosnap checkpoints

### Synopsis

Create Git branches and copy autosnap checkpoint refs between branch namespaces.

Autosnap checkpoints are stored as refs under refs/autosnapshots/<branch>/.
Branch commands copy those refs. They do not duplicate checkpoint commit
objects, apply checkpoint patches, or push anything to a remote.

### Options

```
  -h, --help   help for branch
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees
* [autosnap branch copy](autosnap_branch_copy.md)	 - Copy checkpoint refs between autosnap branch namespaces
* [autosnap branch create](autosnap_branch_create.md)	 - Create and check out a Git branch with checkpoint refs

