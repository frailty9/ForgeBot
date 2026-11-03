# Third-party notices

ForgeBot is licensed under [Apache-2.0](LICENSE). Third-party components retain their own licenses and copyright notices; the project license does not replace them.

The current application dependency list was checked with `go list -deps` on 2026-11-03:

| Component | Version | License / notice copy |
| --- | --- | --- |
| github.com/yuin/goldmark | v1.7.13 | [MIT](docs/third-party-licenses/goldmark.txt), Copyright (c) 2019 Yusuke Inuzuka |
| golang.org/x/text | v0.28.0 | [BSD-style license](docs/third-party-licenses/go-text.txt), Copyright 2009 The Go Authors |
| gopkg.in/yaml.v3 | v3.0.1 | [Original combined MIT and Apache notice](docs/third-party-licenses/yaml-v3.txt), including the libyaml port notices |
| Go standard library / runtime | build toolchain: go1.25.5 | [Go license](docs/third-party-licenses/go-runtime.txt) |

These files preserve the license text from the locally resolved module versions and Go toolchain. When dependencies or distribution contents change, recheck this list and ship the relevant notices with the release. This is a dependency notice record, not a claim that arbitrary downstream additions have been audited.
