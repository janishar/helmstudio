//! Shared text machinery: NeMo's character classes (`en/graph_utils.py`), Python-compatible string helpers backed
//! by frozen CPython 3.12 / Unicode 15.0 tables, and the embedded TSV grammar tables.

use std::sync::LazyLock;

/// Tropical weights in micro-units: 1.1 → 1_100_000. Every NeMo weight is a multiple of 0.00001, so sums are exact.
pub type Cost = i64;
pub const ONE: Cost = 1_000_000;

/// A NeMo weight literal as a `Cost`: `cost(1.1) == 1_100_000`, `cost(-0.0001) == -100` (rounded to the micro-unit).
pub const fn cost(x: f64) -> Cost {
    let v = x * ONE as f64;
    (if v >= 0.0 { v + 0.5 } else { v - 0.5 }) as Cost
}

/// `NEMO_WHITE_SPACE` from `en/graph_utils.py`: `" "`, `\t`, `\n`, `\r`, U+00A0 — **not** Python's `isspace`.
#[inline]
pub fn is_nemo_space(c: char) -> bool {
    matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}')
}

#[inline]
pub fn is_digit(c: char) -> bool {
    c.is_ascii_digit()
}

#[inline]
pub fn is_alpha(c: char) -> bool {
    c.is_ascii_alphabetic()
}

#[inline]
pub fn is_upper(c: char) -> bool {
    c.is_ascii_uppercase()
}

#[inline]
pub fn is_lower(c: char) -> bool {
    c.is_ascii_lowercase()
}

/// `string.punctuation`.
pub const ASCII_PUNCT: &str = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~";

#[inline]
pub fn is_ascii_punct(c: char) -> bool {
    c.is_ascii_punctuation()
}

fn parse_hex_list(src: &str) -> Vec<u32> {
    src.lines()
        .filter(|l| !l.is_empty() && !l.starts_with('#'))
        .map(|l| u32::from_str_radix(l.trim(), 16).expect("hex code point"))
        .collect()
}

fn parse_hex_ranges(src: &str) -> Vec<(u32, u32)> {
    src.lines()
        .filter(|l| !l.is_empty() && !l.starts_with('#'))
        .map(|l| {
            let (a, b) = l.split_once('\t').expect("range row");
            (u32::from_str_radix(a, 16).unwrap(), u32::from_str_radix(b, 16).unwrap())
        })
        .collect()
}

static PY_SPACE: LazyLock<Vec<u32>> = LazyLock::new(|| parse_hex_list(include_str!("../data/unicode/isspace.txt")));
static ND_RANGES: LazyLock<Vec<(u32, u32)>> =
    LazyLock::new(|| parse_hex_ranges(include_str!("../data/unicode/nd_ranges.txt")));
static PUNCT_RANGES: LazyLock<Vec<(u32, u32)>> =
    LazyLock::new(|| parse_hex_ranges(include_str!("../data/unicode/punct_ranges.txt")));

fn in_ranges(ranges: &[(u32, u32)], c: char) -> bool {
    let cp = c as u32;
    let i = ranges.partition_point(|&(_, hi)| hi < cp);
    i < ranges.len() && ranges[i].0 <= cp
}

/// Python `str.isspace()` (CPython 3.12).
#[inline]
pub fn is_py_space(c: char) -> bool {
    if c.is_ascii() {
        return matches!(c, ' ' | '\t' | '\n' | '\r' | '\x0b' | '\x0c' | '\x1c'..='\x1f');
    }
    PY_SPACE.binary_search(&(c as u32)).is_ok()
}

/// Python regex `\d` for `str` patterns: Unicode category `Nd`.
#[inline]
pub fn is_py_digit(c: char) -> bool {
    if c.is_ascii() {
        return c.is_ascii_digit();
    }
    in_ranges(&ND_RANGES, c)
}

/// Unicode general category `P*` (Unicode 15.0).
#[inline]
pub fn is_unicode_punct(c: char) -> bool {
    in_ranges(&PUNCT_RANGES, c)
}

/// First code point of the `Nd` range holding `c` (Unicode digit blocks are aligned runs of 0..9).
pub fn nd_range_start(c: char) -> Option<u32> {
    let cp = c as u32;
    let r = &*ND_RANGES;
    let i = r.partition_point(|&(_, hi)| hi < cp);
    (i < r.len() && r[i].0 <= cp).then(|| r[i].0)
}

/// Python `str.strip()`.
pub fn py_strip(s: &str) -> &str {
    s.trim_matches(is_py_space)
}

/// Python `str.split()` (no argument): split on `isspace` runs, drop empties.
pub fn py_split_ws(s: &str) -> Vec<&str> {
    s.split(is_py_space).filter(|p| !p.is_empty()).collect()
}

/// Leading whitespace prefix (Python `s[:len(s)-len(s.lstrip())]`).
pub fn py_leading_ws(s: &str) -> &str {
    &s[..s.len() - s.trim_start_matches(is_py_space).len()]
}

/// Trailing whitespace suffix (Python `s[len(s.rstrip()):]`).
pub fn py_trailing_ws(s: &str) -> &str {
    &s[s.trim_end_matches(is_py_space).len()..]
}

/// `re.sub(" {2,}", " ", s)`.
pub fn collapse_double_spaces(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut run = 0usize;
    for c in s.chars() {
        if c == ' ' {
            run += 1;
        } else {
            if run > 0 {
                out.push(' ');
                run = 0;
            }
            out.push(c);
        }
    }
    if run > 0 {
        out.push(' ');
    }
    out
}

/// `convert_space`: every ASCII space becomes U+00A0 (tagger values inside quotes).
pub fn space_to_nbsp(s: &str) -> String {
    s.replace(' ', "\u{a0}")
}

/// The inverse (`delete_tokens` / word verbalizer).
pub fn nbsp_to_space(s: &str) -> String {
    s.replace('\u{a0}', " ")
}

/// `pynini.escape`: `\` before `[`, `]`, `\`.
pub fn pynini_escape(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 4);
    for c in s.chars() {
        if matches!(c, '[' | ']' | '\\') {
            out.push('\\');
        }
        out.push(c);
    }
    out
}

/// Python `str.capitalize()` restricted to the ASCII semantics NeMo relies on (first char upper, rest lower).
pub fn py_capitalize(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut chars = s.chars();
    if let Some(c) = chars.next() {
        out.extend(c.to_uppercase());
    }
    for c in chars {
        out.extend(c.to_lowercase());
    }
    out
}

/// One table row: key, value (the key itself for one-column rows), weight.
#[derive(Clone, Debug)]
pub struct Row {
    pub key: String,
    pub value: String,
    pub cost: Cost,
}

/// A grammar table: `pynini.string_file` / `pynini.string_map` rows. Keys are kept sorted for lookup.
pub struct Table {
    rows: Vec<Row>,
    /// Indices into `rows`, sorted by key.
    sorted: Vec<u32>,
    trie: Trie,
}

/// `pynini.string_file` line semantics (OpenFst StringFileCompile, verified in the oracle container): an unescaped
/// `#` starts a comment, `\#` is a literal `#`, trailing whitespace before a comment is trimmed together with it,
/// lines that are empty after that are skipped; columns are split on `\t`; a line ending `\r` keeps the `\r` in its
/// last column.
fn string_file_line(line: &str) -> Option<String> {
    let mut out = String::with_capacity(line.len());
    let mut chars = line.chars().peekable();
    let mut comment = false;
    while let Some(c) = chars.next() {
        match c {
            '\\' if chars.peek() == Some(&'#') => {
                out.push('#');
                chars.next();
            }
            '#' => {
                comment = true;
                break;
            }
            _ => out.push(c),
        }
    }
    if comment {
        let trimmed = out.trim_end_matches([' ', '\t']).len();
        out.truncate(trimmed);
    }
    if out.is_empty() { None } else { Some(out) }
}

/// Python `csv.reader(f, delimiter="\t")` rows (`load_labels`): a field starting with `"` is quoted (may span `\t`
/// and newlines, `""` is a literal quote); other fields are raw. Newlines as `open(..., encoding="utf-8")` with
/// universal newlines: `\r\n` and `\r` end a line.
pub fn csv_rows(src: &str) -> Vec<Vec<String>> {
    let src = src.replace("\r\n", "\n").replace('\r', "\n");
    let chars: Vec<char> = src.chars().collect();
    let mut rows = Vec::new();
    let mut row: Vec<String> = Vec::new();
    let mut field = String::new();
    let mut i = 0;
    let mut at_field_start = true;
    let mut in_quotes = false;
    let mut row_started = false;
    while i < chars.len() {
        let c = chars[i];
        if in_quotes {
            if c == '"' {
                if chars.get(i + 1) == Some(&'"') {
                    field.push('"');
                    i += 2;
                    continue;
                }
                in_quotes = false;
            } else {
                field.push(c);
            }
            i += 1;
            continue;
        }
        match c {
            '"' if at_field_start => {
                in_quotes = true;
                at_field_start = false;
                row_started = true;
            }
            '\t' => {
                row.push(std::mem::take(&mut field));
                at_field_start = true;
                row_started = true;
            }
            '\n' => {
                if row_started || !field.is_empty() {
                    row.push(std::mem::take(&mut field));
                }
                rows.push(std::mem::take(&mut row));
                at_field_start = true;
                row_started = false;
            }
            _ => {
                field.push(c);
                at_field_start = false;
                row_started = true;
            }
        }
        i += 1;
    }
    if in_quotes || row_started || !field.is_empty() {
        row.push(field);
        rows.push(row);
    }
    rows
}

/// Parse a weight column as `Cost` (decimal with ≤ 6 fractional digits).
pub fn parse_cost(s: &str) -> Cost {
    let s = s.trim();
    let (neg, s) = match s.strip_prefix('-') {
        Some(r) => (true, r),
        None => (false, s),
    };
    let (whole, frac) = s.split_once('.').unwrap_or((s, ""));
    let mut micro: i64 = whole.parse::<i64>().unwrap_or(0) * ONE;
    let mut scale = ONE / 10;
    for d in frac.bytes() {
        micro += (d - b'0') as i64 * scale;
        scale /= 10;
    }
    if neg { -micro } else { micro }
}

impl Table {
    /// `pynini.string_file(path)`.
    pub fn parse(src: &str) -> Table {
        let mut rows = Vec::new();
        for raw in src.split('\n') {
            let Some(line) = string_file_line(raw) else { continue };
            let mut cols = line.split('\t');
            let key = cols.next().unwrap_or("").to_string();
            let value = cols.next().map(str::to_string).unwrap_or_else(|| key.clone());
            let cost = cols.next().map(parse_cost).unwrap_or(0);
            rows.push(Row { key, value, cost });
        }
        Table::from_rows(rows)
    }

    pub fn from_rows(rows: Vec<Row>) -> Table {
        let mut sorted: Vec<u32> = (0..rows.len() as u32).collect();
        sorted.sort_by(|&a, &b| rows[a as usize].key.cmp(&rows[b as usize].key).then(a.cmp(&b)));
        let mut trie = Trie::default();
        for (i, r) in rows.iter().enumerate() {
            trie.insert(&r.key, i as u32);
        }
        Table { rows, sorted, trie }
    }

    /// `pynini.string_map` over `(key, value)` pairs (weight 0).
    pub fn from_pairs<K: Into<String>, V: Into<String>>(pairs: impl IntoIterator<Item = (K, V)>) -> Table {
        Table::from_rows(pairs.into_iter().map(|(k, v)| Row { key: k.into(), value: v.into(), cost: 0 }).collect())
    }

    pub fn rows(&self) -> &[Row] {
        &self.rows
    }

    pub fn is_empty(&self) -> bool {
        self.rows.is_empty()
    }

    /// Every row whose key equals `key`, in file order.
    pub fn exact<'a>(&'a self, key: &str) -> impl Iterator<Item = &'a Row> {
        let lo = self.sorted.partition_point(|&i| self.rows[i as usize].key.as_str() < key);
        self.sorted[lo..].iter().map(|&i| &self.rows[i as usize]).take_while(move |r| r.key == key)
    }

    pub fn contains_key(&self, key: &str) -> bool {
        self.exact(key).next().is_some()
    }

    /// Every row whose key matches `text[start..]` (as chars), with the match end: shorter keys first, file order
    /// within one key.
    pub fn prefix_matches<'a>(&'a self, text: &'a [char], start: usize) -> impl Iterator<Item = (&'a Row, usize)> + 'a {
        let mut node = 0usize;
        let mut pos = start;
        let mut pending: std::slice::Iter<'a, u32> = self.trie.nodes[0].rows.iter();
        let mut done = false;
        std::iter::from_fn(move || {
            loop {
                if let Some(&i) = pending.next() {
                    return Some((&self.rows[i as usize], pos));
                }
                if done || pos >= text.len() {
                    return None;
                }
                match self.trie.child(node, text[pos]) {
                    Some(n) => {
                        node = n;
                        pos += 1;
                        pending = self.trie.nodes[n].rows.iter();
                    }
                    None => done = true,
                }
            }
        })
    }
}

/// Char trie over table keys; each node lists the rows whose key ends there, in file order.
#[derive(Default)]
struct Trie {
    nodes: Vec<TrieNode>,
}

#[derive(Default)]
struct TrieNode {
    /// Sorted by char.
    children: Vec<(char, u32)>,
    rows: Vec<u32>,
}

impl Trie {
    fn insert(&mut self, key: &str, row: u32) {
        if self.nodes.is_empty() {
            self.nodes.push(TrieNode::default());
        }
        let mut n = 0usize;
        for c in key.chars() {
            n = match self.child(n, c) {
                Some(next) => next,
                None => {
                    let next = self.nodes.len();
                    self.nodes.push(TrieNode::default());
                    let ch = &mut self.nodes[n].children;
                    let at = ch.partition_point(|&(k, _)| k < c);
                    ch.insert(at, (c, next as u32));
                    next
                }
            };
        }
        self.nodes[n].rows.push(row);
    }

    #[inline]
    fn child(&self, n: usize, c: char) -> Option<usize> {
        let ch = &self.nodes.get(n)?.children;
        ch.binary_search_by(|&(k, _)| k.cmp(&c)).ok().map(|i| ch[i].1 as usize)
    }
}

/// Number of chars of `needle` matched at `text[start..]`, if all of it matches.
#[inline]
pub fn match_str(text: &[char], start: usize, needle: &str) -> Option<usize> {
    let mut i = start;
    for c in needle.chars() {
        if i >= text.len() || text[i] != c {
            return None;
        }
        i += 1;
    }
    Some(i - start)
}

/// Whether `text[start..]` begins with `needle` (as chars).
#[inline]
pub fn starts_with(text: &[char], start: usize, needle: &str) -> bool {
    match_str(text, start, needle).is_some()
}

/// Declare a lazily parsed embedded table with `pynini.string_file` semantics.
#[macro_export]
macro_rules! tsv {
    ($name:ident, $path:literal) => {
        pub static $name: ::std::sync::LazyLock<$crate::text::Table> = ::std::sync::LazyLock::new(|| {
            $crate::text::Table::parse(include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/", $path)))
        });
    };
}

/// Declare lazily parsed embedded rows with `load_labels` (`csv.reader`) semantics.
#[macro_export]
macro_rules! labels {
    ($name:ident, $path:literal) => {
        pub static $name: ::std::sync::LazyLock<Vec<Vec<String>>> = ::std::sync::LazyLock::new(|| {
            $crate::text::csv_rows(include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/", $path)))
        });
    };
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn python_space_table() {
        assert!(is_py_space('\u{3000}'));
        assert!(is_py_space('\u{a0}'));
        assert!(is_py_space('\x1c'));
        assert!(!is_py_space('\u{200b}'));
        assert_eq!(py_strip("\u{3000}a b \n"), "a b");
        assert_eq!(py_split_ws(" a\u{3000}b  c "), vec!["a", "b", "c"]);
    }

    #[test]
    fn unicode_classes() {
        assert!(is_unicode_punct('。'));
        assert!(is_unicode_punct('—'));
        assert!(!is_unicode_punct('$'));
        assert!(is_py_digit('٣'));
        assert!(!is_py_digit('²'));
    }

    #[test]
    fn costs() {
        assert_eq!(parse_cost("-0.0001"), -100);
        assert_eq!(parse_cost("1.1"), 1_100_000);
        assert_eq!(parse_cost("0.00001"), 10);
        assert_eq!(cost(1.1), 1_100_000);
        assert_eq!(cost(-0.0001), -100);
        assert_eq!(cost(0.00002), 20);
        assert_eq!(cost(-1000.0), -1_000_000_000);
        assert_eq!(collapse_double_spaces("a  b   c "), "a b c ");
    }

    #[test]
    fn string_file_semantics() {
        // pynini 2.1.6 StringFileCompile, checked in the oracle container
        let t =
            Table::parse("#\tnumber sign\nC#\tc sharp\nx \t#c\ne\\#f\tg\na\tb\r\nc\td\t-0.5\n\"\tinch\nk\n\tempty\n");
        assert!(t.exact("#").next().is_none());
        assert_eq!(t.exact("C").next().unwrap().value, "C");
        assert_eq!(t.exact("x").next().unwrap().value, "x");
        assert_eq!(t.exact("e#f").next().unwrap().value, "g");
        assert_eq!(t.exact("a").next().unwrap().value, "b\r");
        assert_eq!(t.exact("c").next().unwrap().cost, -500_000);
        assert_eq!(t.exact("\"").next().unwrap().value, "inch");
        assert_eq!(t.exact("k").next().unwrap().value, "k");
        assert_eq!(t.exact("").next().unwrap().value, "empty");
        let chars: Vec<char> = "ab".chars().collect();
        assert_eq!(t.prefix_matches(&chars, 0).count(), 2); // "a" and the empty key
    }

    #[test]
    fn csv_semantics() {
        assert_eq!(csv_rows("#\thash\nC#\tc sharp\r\n"), vec![vec!["#", "hash"], vec!["C#", "c sharp"]]);
        assert_eq!(csv_rows("\"\tinch\nb\tc\n"), vec![vec!["\tinch\nb\tc\n"]]);
        assert_eq!(csv_rows("a\"b\tc\n\nd\n"), vec![vec!["a\"b", "c"], vec![], vec!["d"]]);
    }
}
