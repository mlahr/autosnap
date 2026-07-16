# autosnap restart

## autosnap restart

Restart the autosnap daemon with the current configuration

### Synopsis

Restart the running autosnap daemon with the current .autosnap.toml.

Configuration flags supplied to the original autosnap start remain overrides.
To change those overrides, run autosnap stop and then autosnap start with the
new flags. Detailed restart progress is appended to the autosnap daemon log.

```
autosnap restart [flags]
```

### Options

```
  -h, --help   help for restart
```

### SEE ALSO

* [autosnap](autosnap.md)	 - Local checkpointing for Git worktrees

