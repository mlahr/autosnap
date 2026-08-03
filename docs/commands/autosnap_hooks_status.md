# autosnap hooks status

## autosnap hooks status

Show autosnap Git hook status

For each hook, the command reports whether the hook is absent, installed,
installed and chained to a pre-existing hook, modified, conflicting, or affected
by a filesystem error. Problem states include the exact path, the detected
reason, and a safe resolution. In particular, an existing hook can be preserved
by running `autosnap hooks install --force`, which moves it to an autosnap backup
and chains it after the autosnap hook. Modified autosnap-managed hooks and
orphaned backups require manual review; autosnap does not overwrite them.

```
autosnap hooks status [flags]
```

### Options

```
  -h, --help   help for status
```

### SEE ALSO

* [autosnap hooks](autosnap_hooks.md)	 - Manage autosnap Git hooks
