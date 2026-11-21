//! `it/taggers/money.py` (deterministic).

use std::sync::LazyLock;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Row, cost, is_nemo_space};

use super::cardinal;
use super::decimals;

crate::tsv!(MAJ, "it/money/currency_major.tsv");
crate::tsv!(MIN, "it/money/currency_minor.tsv");
crate::tsv!(FEM_PLURAL, "it/money/currency_plural_fem.tsv");
crate::tsv!(MASC_PLURAL, "it/money/currency_plural_masc.tsv");

const MINOR_W: Cost = cost(-0.001);
const NO_MIN_W: Cost = cost(0.0001);

/// `singular @ (fem_plural | masc_plural)`.
fn plural(singular: &str) -> Option<&'static str> {
    let t: [&'static crate::text::Table; 2] = [&FEM_PLURAL, &MASC_PLURAL];
    t.into_iter().flat_map(|t| t.rows().iter()).find(|r| r.key == singular).map(|r| r.value.as_str())
}

/// `pynini.closure(delete_space, 0, 1)`: every end of a whitespace run (including none).
fn ws_ends(text: &[char], p: usize) -> impl Iterator<Item = usize> + '_ {
    let mut e = p;
    while e < text.len() && is_nemo_space(text[e]) {
        e += 1;
    }
    p..=e
}

/// A currency key at `p`.
fn majors<'a>(text: &'a [char], p: usize) -> impl Iterator<Item = (&'a Row, usize)> + 'a {
    MAJ.prefix_matches(text, p)
}

/// `graph_decimal_plural` / `graph_decimal_singular` input filters on the whole money span.
fn plural_ok(s: &[char]) -> bool {
    s.iter().enumerate().any(|(i, &c)| c == ',' && !(i == 1 && s[0] == '1'))
}
fn singular_ok(s: &[char]) -> bool {
    s.len() >= 2 && s[0] == '1' && s[1] == ','
}

/// `two_digits_fractional_part` value of a digit string given its first two digits and the length up to its last
/// non-zero digit (trailing zeros stripped; one or two digits remain).
fn two_digits(head: [u8; 2], stripped: usize) -> Option<([u8; 2], usize)> {
    match (stripped, head) {
        (1, [d, _]) => Some(([d, b'0'], 2)),
        (2, [b'0', d]) => Some(([d, 0], 1)),
        (2, [a, b]) if b != b'0' => Some(([a, b], 2)),
        _ => None,
    }
}

pub struct Money {
    pub cost: Cost,
}

/// Writes a part of the tag body.
type Body<'a> = &'a dyn Fn(&mut String);

impl Money {
    /// `money { <body> }`.
    fn push(&self, out: &mut Vec<Candidate>, end: usize, c: Cost, body: Body) {
        let tag = super::tag(end, 128, |t| {
            t.push_str("money { ");
            body(t);
            t.push_str(" }");
        });
        out.push(Candidate { end, cost: self.cost + c, rank: 0, tag });
    }

    /// `graph_integer_only` and the decimal branches.
    fn major(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let digit_start = text[start].is_ascii_digit();
        let currency_start = currency_at(text, start);
        if !digit_start && !currency_start {
            return;
        }
        // currency first; integers: graph_integer (cardinal.graph on anything but "1"), graph_integer_one ("1")
        if currency_start {
            for (row, e) in majors(text, start) {
                let pl = plural(&row.value);
                let cur_field = |t: &mut String, cur: &str| {
                    t.push_str("currency_maj: \"");
                    t.push_str(cur);
                    t.push_str("\" ");
                };
                for q in ws_ends(text, e) {
                    cardinal::spans(text, q, |n| {
                        if n.digits() == b"1" {
                            self.push(out, n.end, 0, &|t| {
                                cur_field(t, &row.value);
                                t.push_str("integer_part: \"un\"");
                            });
                        } else if let Some(pl) = pl {
                            let Some(c) = cardinal::cost_of(n.digits()) else { return };
                            self.push(out, n.end, c, &|t| {
                                cur_field(t, pl);
                                t.push_str("integer_part: \"");
                                cardinal::words_into(n.digits(), t);
                                t.push('"');
                            });
                        }
                    });
                    for d in decimals::readings(text, q).iter() {
                        let span = &text[start..d.end];
                        if singular_ok(span) {
                            self.push(out, d.end, d.cost, &|t| {
                                cur_field(t, &row.value);
                                d.fields(text, t);
                            });
                        }
                        if let Some(pl) = pl {
                            if plural_ok(span) || (d.has_quantity() && text[d.end - 1].is_ascii_alphabetic()) {
                                self.push(out, d.end, d.cost, &|t| {
                                    cur_field(t, pl);
                                    d.fields(text, t);
                                });
                            }
                        }
                    }
                }
            }
        }
        if !digit_start {
            return;
        }
        // amount first: the currency key must follow the amount's run of digits, separators and whitespace
        let mut run = start;
        while run < text.len()
            && (text[run].is_ascii_digit() || matches!(text[run], '.' | ',') || is_nemo_space(text[run]))
        {
            run += 1;
        }
        if !currency_in(text, start + 1, run) {
            return;
        }
        #[derive(Clone, Copy, PartialEq)]
        enum Kind {
            One,
            Int,
            Dec,
        }
        let suffixed = |out: &mut Vec<Candidate>, end: usize, c: Cost, fields: Body, kind: Kind| {
            for q in ws_ends(text, end) {
                for (row, e) in majors(text, q) {
                    let span = &text[start..e];
                    let pl = plural(&row.value);
                    let mut emit = |cur: &str| {
                        self.push(out, e, c, &|t| {
                            fields(t);
                            t.push_str(" currency_maj: \"");
                            t.push_str(cur);
                            t.push('"');
                        });
                    };
                    match kind {
                        Kind::Dec => {
                            if singular_ok(span) {
                                emit(&row.value);
                            }
                            if let Some(pl) = pl.filter(|_| plural_ok(span)) {
                                emit(pl);
                            }
                        }
                        Kind::One => emit(&row.value),
                        Kind::Int => {
                            if let Some(pl) = pl {
                                emit(pl);
                            }
                        }
                    }
                }
            }
        };
        let currency_after = |end: usize| ws_ends(text, end).any(|q| currency_at(text, q));
        cardinal::spans(text, start, |n| {
            if !currency_after(n.end) {
                return;
            }
            if n.digits() == b"1" {
                suffixed(out, n.end, 0, &|t| t.push_str("integer_part: \"un\""), Kind::One);
            } else if let Some(c) = cardinal::cost_of(n.digits()) {
                let body = |t: &mut String| {
                    t.push_str("integer_part: \"");
                    cardinal::words_into(n.digits(), t);
                    t.push('"');
                };
                suffixed(out, n.end, c, &body, Kind::Int);
            }
        });
        for d in decimals::readings(text, start).iter().filter(|d| currency_after(d.end)) {
            suffixed(out, d.end, d.cost, &|t| d.fields(text, t), Kind::Dec);
        }
    }

    /// The currency-independent part of `decimal_graph_with_minor_curr` at `p`: integer readings (plain digits, not
    /// "0", followed by `,`; `None` for "1" → "un" + singular, else the digits and their cardinal weight), each
    /// with the start of its fractional digits (after the `,`).
    fn minor_amounts(text: &[char], p: usize, mut f: impl FnMut(Option<(&cardinal::Num, Cost)>, usize)) {
        cardinal::plain_spans(text, p, |n| {
            let ds = n.digits();
            if ds == b"0" || text.get(n.end) != Some(&',') {
                return;
            }
            if ds == b"1" {
                f(None, n.end + 1);
            } else if let Some(c) = cardinal::cost_of(ds) {
                f(Some((n, c)), n.end + 1);
            }
        });
    }

    /// `decimal_graph_with_minor_curr` for the currency `cur` at `p`; `f(end, cost, body)`.
    fn minor_body(text: &[char], p: usize, cur: &Currency, f: &mut dyn FnMut(usize, Cost, &dyn Fn(&mut String))) {
        // fractional_plus_min
        let with_min =
            |fr: &Frac, f: &mut dyn FnMut(usize, Cost, &dyn Fn(&mut String)), head: &dyn Fn(&mut String), c0: Cost| {
                let Some(min) = cur.min else { return };
                let name = if fr.one {
                    min
                } else if let Some(pl) = cur.min_pl {
                    pl
                } else {
                    return;
                };
                let c = if fr.one { 0 } else { fr.plain_cost };
                f(fr.end, c0 + c, &|o: &mut String| {
                    head(o);
                    o.push_str("fractional_part: \"");
                    if fr.one {
                        o.push_str("un");
                    } else {
                        fr.plain(o);
                    }
                    o.push_str("\" currency_min: \"");
                    o.push_str(name);
                    o.push('"');
                });
            };
        Self::minor_amounts(text, p, |int, frac_at| {
            let (ic, maj) = match int {
                None => (0, cur.maj),
                Some((_, c)) => match cur.maj_pl {
                    Some(pl) => (c, pl),
                    None => return,
                },
            };
            let head = |o: &mut String| {
                o.push_str("integer_part: \"");
                match int {
                    None => o.push_str("un"),
                    Some((n, _)) => {
                        cardinal::words_into(n.digits(), o);
                    }
                }
                o.push_str("\" currency_maj: \"");
                o.push_str(maj);
                o.push_str("\" ");
            };
            fractions(text, frac_at, |fr| with_min(fr, f, &head, ic));
            // weight 0.0001: fractional without minor currency (`two_digit_no_zero`, "1" included)
            fractions(text, frac_at, |fr| {
                f(fr.end, ic + fr.plain_cost + NO_MIN_W, &|o: &mut String| {
                    head(o);
                    o.push_str("fractional_part: \"");
                    fr.plain(o);
                    o.push('"');
                });
            });
        });
        // delete("0,") + fractional_plus_min
        if text.get(p) == Some(&'0') && text.get(p + 1) == Some(&',') {
            fractions(text, p + 2, |fr| with_min(fr, f, &|_: &mut String| {}, 0));
        }
    }

    fn minor(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let digit_start = text[start].is_ascii_digit();
        if !digit_start && !currency_at(text, start) {
            return;
        }
        let emit = |out: &mut Vec<Candidate>, end: usize, c: Cost, body: &dyn Fn(&mut String)| {
            let tag = super::tag(end, 96, |t| {
                t.push_str("money { ");
                body(t);
                t.push_str(" preserve_order: true }");
            });
            out.push(Candidate { end, cost: self.cost + c + MINOR_W, rank: 0, tag });
        };
        // prefix symbol: symbol, optional whitespace, body
        if !digit_start {
            for (row, e) in majors(text, start) {
                let cur = currency(row);
                for q in ws_ends(text, e) {
                    if text.get(q).is_some_and(char::is_ascii_digit) {
                        Self::minor_body(text, q, cur, &mut |end, c, b| emit(out, end, c, b));
                    }
                }
            }
            return;
        }
        // suffix symbol: body, optional whitespace, symbol (directly after the digits, `,` and whitespace run);
        // the body needs a `,` right after the leading digits
        let mut num_end = start;
        while num_end < text.len() && text[num_end].is_ascii_digit() {
            num_end += 1;
        }
        if text.get(num_end) != Some(&',') {
            return;
        }
        while num_end < text.len()
            && (text[num_end].is_ascii_digit() || text[num_end] == ',' || is_nemo_space(text[num_end]))
        {
            num_end += 1;
        }
        if !currency_in(text, start + 1, num_end) {
            return;
        }
        // the currency rows matching somewhere in that run
        let mut present = 0u64;
        for q in start + 1..=num_end {
            if currency_at(text, q) {
                for (r, _) in majors(text, q) {
                    present |= 1 << currency_index(r);
                }
            }
        }
        for (i, row) in MAJ.rows().iter().enumerate() {
            if present & (1 << i) == 0 {
                continue;
            }
            let sym = row.key.as_str();
            let len = sym.chars().count();
            Self::minor_body(text, start, &CURRENCIES[i], &mut |end, c, b| {
                for q in ws_ends(text, end) {
                    if crate::text::starts_with(text, q, sym) {
                        emit(out, q + len, c, b);
                    }
                }
            });
        }
    }
}

/// Per `currency_major.tsv` row (same index): the major name, its plural, the minor name and its plural.
struct Currency {
    maj: &'static str,
    maj_pl: Option<&'static str>,
    min: Option<&'static str>,
    min_pl: Option<&'static str>,
}

static CURRENCIES: LazyLock<Vec<Currency>> = LazyLock::new(|| {
    MAJ.rows()
        .iter()
        .map(|r| {
            let min = MIN.exact(&r.key).next().map(|r| r.value.as_str());
            Currency { maj: &r.value, maj_pl: plural(&r.value), min, min_pl: min.and_then(plural) }
        })
        .collect()
});

/// The row index of a `currency_major.tsv` row.
fn currency_index(r: &Row) -> usize {
    MAJ.rows().iter().position(|x| std::ptr::eq(x, r)).expect("a currency_major row")
}

fn currency(r: &Row) -> &'static Currency {
    &CURRENCIES[currency_index(r)]
}

/// The first chars of the currency keys.
static FIRST: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = MAJ.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

thread_local! {
    /// `NEXT[p]`: the first position `q ≥ p` where a currency key starts in the current input (`len` if none).
    static NEXT: std::cell::RefCell<Vec<u32>> = const { std::cell::RefCell::new(Vec::new()) };
}

/// Start of a new input: where currency keys start (the union calls this at position 0).
pub fn reset(text: &[char]) {
    NEXT.with_borrow_mut(|next| {
        next.clear();
        next.resize(text.len() + 1, text.len() as u32);
        for p in (0..text.len()).rev() {
            let starts = FIRST.binary_search(&text[p]).is_ok() && majors(text, p).next().is_some();
            next[p] = if starts { p as u32 } else { next[p + 1] };
        }
    });
}

/// Whether a currency key starts somewhere in `a..=b` (positions past the text never do).
pub fn currency_in(text: &[char], a: usize, b: usize) -> bool {
    if a >= text.len() {
        return false;
    }
    NEXT.with_borrow(|next| {
        if next.len() == text.len() + 1 {
            next[a] as usize <= b
        } else {
            // not prepared for this text (a classifier queried directly)
            (a..=b.min(text.len() - 1)).any(|q| majors(text, q).next().is_some())
        }
    })
}

/// Whether a currency key starts at `p`.
pub fn currency_at(text: &[char], p: usize) -> bool {
    currency_in(text, p, p)
}

/// One `two_digits_fractional_part` reading ending at `end`.
struct Frac {
    end: usize,
    /// "1" (→ `un` + singular minor currency).
    one: bool,
    /// The value's digits (one or two), read by `cardinal.two_digit_no_zero` (for "1" too: the no-minor branch
    /// reads it as "uno").
    value: [u8; 2],
    len: usize,
    plain_cost: Cost,
}

impl Frac {
    /// `cardinal.two_digit_no_zero` of the value.
    fn plain(&self, o: &mut String) {
        let mut c = 0;
        cardinal::two_digit_no_zero(&self.value[..self.len], o, &mut c);
    }
}

/// `two_digits_fractional_part` readings of the digit run at `p`, by end.
fn fractions(text: &[char], p: usize, mut f: impl FnMut(&Frac)) {
    let mut head = [b'0'; 2];
    let mut stripped = 0;
    let mut e = p;
    while e < text.len() && text[e].is_ascii_digit() {
        let c = text[e] as u8;
        if e - p < 2 {
            head[e - p] = c;
        }
        e += 1;
        if c != b'0' {
            stripped = e - p;
        }
        let Some((value, len)) = two_digits(head, stripped) else { continue };
        // two_digit_no_zero: one digit 1-9, "0" + digit, or graph_tens
        let plain_cost = match value[..len] {
            [_] => 0,
            [b'0', _] => 0,
            [t, u] => match cardinal::graph_tens_cost(t, u) {
                Some(c) => c,
                None => continue,
            },
            _ => continue,
        };
        f(&Frac { end: e, one: value[..len] == *b"1", value, len, plain_cost });
    }
}

impl Classifier for Money {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.major(text, start, out);
        self.minor(text, start, out);
    }
}
