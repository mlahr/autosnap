# autosnap branch copy

## autosnap branch copy

Copy checkpoint refs between autosnap branch namespaces

### Synopsis

Copy autosnap checkpoint refs from one branch namespace to another.

The target Git branch must already exist. This command does not check out or
create Git branches.

```
autosnap branch copy [flags]
```

### Examples

```
autosnap branch copy --from main --to feature/next
autosnap branch copy --from main --to feature/next --overwrite
```

### Options

```
      --from string   Source autosnap branch namespace
  -h, --help          help for copy
      --overwrite     Replace colliding target checkpoint refs
      --to string     Target autosnap branch namespace
```

### SEE ALSO

* [autosnap branch](autosnap_branch.md)	 - Create Git branches with autosnap checkpoints

