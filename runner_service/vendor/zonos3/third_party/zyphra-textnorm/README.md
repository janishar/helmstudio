# zyphra-textnorm

Text normalization for text-to-speech: written text to its spoken form ("$5.32" → "five dollars thirty two cents"),
for 43 languages. The original nine packages are hand-written Rust ports of the
[NeMo-text-processing](https://github.com/NVIDIA/NeMo-text-processing) r1.2.0 grammars; the additional 34 port
ZONOS3's production locale grammars and Unicode CLDR release-49 data. There is no FST runtime and no pynini: every
grammar is Rust code, every table is embedded at compile time, and nothing is loaded at runtime. One library serves
Rust, Python and C, with a 1024-entry memo per normalizer.

The differential corpora contain 179,669 rows: 92,953 for the original nine languages and 86,716 for the added
locales, generated from vendored fixtures, lexical tables, and held-out rule boundaries (see [Parity](#parity)).

## Use

```rust
use zyphra_textnorm::Normalizer;

assert_eq!(zyphra_textnorm::normalize("It costs $5.32.", "en"), "It costs five dollars thirty two cents.");
let tn = Normalizer::with_markup_tags(["laugh", "shouting"]);
assert_eq!(tn.normalize("So I said <laugh/> no way, $5.", "en"), "So I said <laugh/> no way, five dollars.");
```

```python
import zyphra_textnorm

zyphra_textnorm.normalize("Am 3. Oktober kostete es 5,32 €.", "de")
# 'Am dritter oktober kostete es fünf komma drei zwei €.'
tn = zyphra_textnorm.Normalizer(markup_tags=["laugh"])
tn.normalize("So I said <laugh/> no way, $5.", "en")
```

```c
#include "zyphra_textnorm.h"

zyphra_textnorm *tn = zyphra_textnorm_new("laugh\nshouting", 14);
char *out = zyphra_textnorm_normalize(tn, text, text_len, "en", 2);
/* ... */
zyphra_textnorm_free(out);
zyphra_textnorm_delete(tn);
```

- **Languages**: `LANGUAGES` = `en de es fr it pt ja zh ko nl pl tr uk cs da fi el nb ro sk bg he th tl ms ka ta gu kn ml pa or ru sv hu ar vi id hi bn te mr ur`.
  `zh` is Mandarin, `nb` is Norwegian Bokmål, `tl` is Tagalog/Filipino, and `or` is Odia. `supported(lang)` says
  whether this build has a grammar. One cargo feature per language (`lang-en` …), all on by default.
- **Never fails**: a language without a grammar comes back with its line breaks collapsed, and a sentence the
  grammar fails on is kept as typed (the rest of the text is still normalized).
- **Line breaks**: every whitespace run holding a line break, tab or other non-space whitespace becomes one space
  first (`collapse_line_breaks`); runs of plain spaces are kept.
- **Markup** (`Normalizer::with_markup_tags`): the markup of the given tag names stays as typed: point tags
  (`<laugh/>`, `<laugh>`) and span tags (`<shouting>…</shouting>`, `\s*` allowed before `/` or `>`), and the text
  between markers is normalized piece by piece. Without tags, `<…>` is text like any other.
- **Verbatim spans**: anything between U+E000 and U+E001 (e.g. a pronunciation) is kept as typed.
- **Memo**: each `Normalizer` remembers its last 1024 results, shared by every thread that calls it;
  `normalize(text, lang)` uses one shared normalizer without markup tags.

The output is the spoken form of the reference grammars, quirks included: parity is the contract. The original
packages retain the frozen NeMo wrapper behavior (`oracle/reference_textnorm.py`); new locale packages use the
locale-gated ZONOS3 wrapper (`oracle/new_locale_wrapper.py`). This covers sentence punctuation, native symbol and
operator words, ordinal periods, numeric conventions, bracket spacing, and markup/verbatim preservation. Numeric
dates follow locale conventions (`tl` uses month/day/year); ISO `YYYY-MM-DD` avoids ambiguity.
Dutch million and billion remainders use the corrected fixed-width source grammar, preventing omitted zero groups
from changing the number's scale.

## Install

| | |
| --- | --- |
| Rust | `cargo add zyphra-textnorm` (MSRV 1.88) |
| Python (≥ 3.9) | `pip install zyphra-textnorm`: abi3 wheels for Linux (manylinux, x86_64 and aarch64), macOS (x86_64, arm64) and Windows (x64, arm64); elsewhere pip builds the sdist, which needs cargo |
| C / C++ | `cargo rustc --release --lib --features capi --crate-type staticlib` → `target/release/libzyphra_textnorm.a` (`zyphra_textnorm.lib` on MSVC), header `include/zyphra_textnorm.h`. Link the platform's Rust std libraries (`-lpthread -ldl -lm` on Linux, `ws2_32 userenv ntdll bcrypt` on MSVC). |

The release profile keeps `panic = "unwind"`: a panic inside a grammar is a grammar failure (the sentence kept as
typed). A build with `panic = "abort"` aborts on one instead.

## Layout

| Path | Contents |
| --- | --- |
| `src/` | The library: `lib.rs` (API), `wrapper.rs` (line breaks, markup, verbatim spans, per-language passes, sentence retry, memo), `pipeline.rs` (NeMo's `Normalizer.normalize`), `lattice.rs` (the tagger's Viterbi), `tag.rs`, `permute.rs`, `moses.rs`, `punct_post.rs`, `ffi.rs` (C ABI) |
| `src/lang/<l>/` | One grammar package: `taggers/` and `verbalizers/` mirror the upstream files one to one |
| `data/` | The upstream `.tsv` tables verbatim, the sacremoses perluniprops tables, frozen Unicode 15.0 tables |
| `include/` | The C header |
| `python/` | The Python extension `zyphra_textnorm` (PyO3; `pyproject.toml` at the root) |
| `oracle/` | The differential-test oracle: vendored NeMo r1.2.0 and ZONOS3 locale packages, reference wrappers, corpus and golden dump scripts |
| `tests/` | Differential tests; the corpus (`corpus/`), the oracle's outputs (`golden/*.jsonl.zst`) and the oracle-confirmed equal-cost ties the port resolves differently (`ties/`) |
| `PORTING.md` | How a NeMo grammar becomes Rust, with the verified pynini semantics |

## Develop

```sh
cargo test --release --features capi       # unit tests + differential tests against the goldens
cargo run --release --example normalize -- en 'Save $20k.'
cargo bench                                # median µs per call and per 100 chars, allocations
cargo bench --no-default-features --features lang-de   # one language, faster to build
uv venv && uv pip install -e . && .venv/bin/python -c 'import zyphra_textnorm'
```

The bench warms every case before timing it and reports the median of 101 calls on a cleared memo. Run it at load
below 4: medians swing by 1.5× on a busy machine. The target is ≤ 20 µs per 100 characters on every language's line.

## Release

The crate and the Python package share the version (`[workspace.package]` in `Cargo.toml`). Pushing a `v<version>`
tag runs `.github/workflows/release.yml`, which builds the wheels and the sdist and installs and runs each wheel on
its platform. Then:

```sh
cargo publish -p zyphra-textnorm                                  # crates.io
gh run download <run id> -D dist && uv publish dist/*/*           # PyPI
```

## Parity

The goldens in `tests/golden/` are outputs of the vendored reference grammars (pynini) behind the locale-gated wrapper
(`oracle/reference_textnorm.py`), committed `zstd -19`-compressed (the tests read the `.zst` directly; the plain
`.jsonl` the dump scripts write are gitignored). The corpus is generated from every table of each language
(`oracle/gen_corpus.py` for the original nine, `oracle/gen_new_corpus.py` for the added locales), upstream test cases,
and handcrafted lines. `oracle/dump_new_goldens.py` compiles one new locale at a time and writes compressed goldens.
The `calls` test checks every grammar call the wrapper made; the `golden` test checks every end-to-end row. The
original corpus has 735 calls where the oracle picks among paths of equal float32 cost by its compiled arc order
(`tests/ties/`, PORTING.md "Ties"); a listed call that no longer differs fails as stale. The English number-name FARs,
which have no source, are checked against a 457k-row dump of the FSTs (`tests/en_cardinal.rs`).

Use a native Python environment with pynini, or the linux/amd64 oracle container (Rosetta on Apple silicon):

```sh
colima start --vz-rosetta --cpu 4 --memory 8
docker build --platform linux/amd64 -f oracle/Dockerfile -t zyphra-textnorm-oracle .
docker run --rm -v "$PWD":/repo -v "$HOME/.cache/zyphra-textnorm-oracle":/cache -w /repo zyphra-textnorm-oracle \
    python oracle/dump_golden.py --cache /cache --lang en --corpus tests/corpus/en.jsonl --out tests/golden/en.jsonl
zstd -19 -f tests/golden/en.jsonl -o tests/golden/en.jsonl.zst
docker run --rm -v "$PWD":/repo -v "$HOME/.cache/zyphra-textnorm-oracle":/cache -w /repo zyphra-textnorm-oracle \
    python oracle/triage.py --lang en --text 'Call me at 123 Main Street, please.'
```

`zstd -d -k tests/golden/<l>.jsonl.zst` restores the plain file for inspection. `oracle/gen_corpus.py` (host Python)
regenerates `tests/corpus/`; `oracle/gen_unicode_tables.py` the Unicode tables (must run on CPython 3.12 / Unicode
15.0, like the oracle).

## License

MIT (`LICENSE`) for original code; NeMo-derived grammars and tables are Apache-2.0, CLDR-derived locale data is
Unicode-3.0, and the Moses detokenizer port is MIT (sacremoses). See `NOTICE` and `licenses/`.
