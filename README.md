# 𝚫t

deltat is a simple command-line [time tracker][Time-tracking software].

```console
% export DELTAT_DB="$HOME/deltat.sqlite"
% deltat start 'Working on issue #12345'
“Working on issue #12345” started at 8:57AM
... some time later, Ctrl-C ...
Ended at 9:32AM
% deltat task edit --label=coding $(deltat task select '#12345')
% deltat timesheet
# 2026-02-01

-  8:57AM –  9:32AM: Working on issue #12345

# Totals

| Task                                                     | Time    |
| :------------------------------------------------------- | ------: |
| Working on issue #12345                                  | 0:35:22 |

By label:

| Label                            | Time    |
| :------------------------------- | ------: |
| coding                           | 0:35:22 |
```

[Time-tracking software]: https://en.wikipedia.org/wiki/Time-tracking_software
[zombiezen]: https://www.zombiezen.com/

## Installation

Using Go:

```shell
go install zombiezen.com/go/deltat@latest
```

Or through [Nix][]:

```shell
nix profile install github:zombiezen/deltat
```

[Nix]: https://nixos.org/

## Status

I ([zombiezen][]) wrote this for my own personal use
and I'm making the source publicly available for my own convenience.
If it's useful to you: yay!
but I will not be providing support or accepting contributions.

## License

[Apache 2.0](LICENSE)
