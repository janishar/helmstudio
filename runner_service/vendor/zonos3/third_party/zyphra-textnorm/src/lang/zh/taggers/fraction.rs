//! `zh/taggers/fraction.py`: `[I 又] N/D`, `D分之N`, `I又D分之N`, `[<suffix>]分之N`, `<decimals>%`, `N%`, `100%`, each
//! also in `graph_with_sign` at −3.0. That sign is `closure(insert("negative: ") + cross("-", "\"负\""))` |
//! `negative: "负"` (for `负`/`負`): the closure's zero repetitions make every body available at −3.0 with just the
//! inserted space (`fraction {  …`), which is always cheaper than the plain union, so only the signed forms are
//! emitted.

use super::{Buf, Gen, ROOT, Sink, after_signed_digits, card, card_ends, card_run, digit_end, digit_names};
use crate::text::{cost, match_str};

const SUFFIX: &[&str] = &[
    "百", "千", "万", "十万", "百万", "千万", "亿", "十亿", "百亿", "千亿", "萬", "十萬", "百萬", "千萬", "億", "十億",
    "百億", "千億", "拾萬", "佰萬", "仟萬", "拾億", "佰億", "仟億", "拾万", "佰万", "仟万", "仟亿", "佰亿",
];

/// One field of a body: a cardinal over `t[a..b]` or a literal value.
#[derive(Clone, Copy)]
enum V {
    Card(usize, usize),
    Lit(&'static str),
}

/// A body reading (without the leading inserted space): end and up to three `key: "value"` fields.
type Body = (usize, [Option<(&'static str, V)>; 3]);

fn write_body(t: &[char], b: &Body, out: &mut String) {
    let mut first = true;
    for (key, v) in b.1.iter().flatten() {
        if !first {
            out.push(' ');
        }
        first = false;
        out.push_str(key);
        out.push_str(": \"");
        match *v {
            V::Card(a, e) => card(t, a, e, out),
            V::Lit(s) => out.push_str(s),
        }
        out.push('"');
    }
}

/// Body readings at `p`, except `graph_decimal_percentage` (see [`decimal_percent`]).
fn bodies(t: &[char], p: usize, out: &mut Vec<Body>) {
    use V::*;
    // every follower below (`又 / 分 %`) is a non-digit
    if let Some(ie) = card_run(t, p) {
        let i = Card(p, ie);
        match t.get(ie) {
            // graph_with_integer (I 又 N / D) and graph_morpheme's second branch (I 又 D 分之 N)
            Some('又') => {
                if let Some(ne) = card_run(t, ie + 1) {
                    let n = Card(ie + 1, ne);
                    if t.get(ne) == Some(&'/') {
                        for de in card_ends(t, ne + 1) {
                            let d = Card(ne + 1, de);
                            out.push((
                                de,
                                [Some(("integer_part", i)), Some(("numerator", n)), Some(("denominator", d))],
                            ));
                        }
                    }
                    if match_str(t, ne, "分之").is_some() {
                        for n2e in card_ends(t, ne + 2) {
                            let n2 = Card(ne + 2, n2e);
                            out.push((
                                n2e,
                                [Some(("integer_part", i)), Some(("denominator", n)), Some(("numerator", n2))],
                            ));
                        }
                    }
                }
            }
            // graph_with_integer without the integer / graph_only_slash: N / D
            Some('/') => {
                for de in card_ends(t, ie + 1) {
                    out.push((de, [Some(("numerator", i)), Some(("denominator", Card(ie + 1, de))), None]));
                }
            }
            // graph_morpheme: D 分之 N
            Some('分') if t.get(ie + 1) == Some(&'之') => {
                for ne in card_ends(t, ie + 2) {
                    out.push((ne, [Some(("denominator", i)), Some(("numerator", Card(ie + 2, ne))), None]));
                }
            }
            // graph_integer_percentage: N % (one repetition of its closure per span)
            Some('%') => out.push((ie + 1, [Some(("numerator", i)), Some(("denominator", Lit("百"))), None])),
            _ => {}
        }
    }
    // graph_with_suffix: [suffix] 分之 N
    let mut sfx: [(usize, Option<&'static str>); 4] = [(p, None); 4];
    let mut k = 1;
    let suffix_first = matches!(t.get(p), Some('百' | '千' | '万' | '十' | '亿' | '萬' | '億' | '拾' | '佰' | '仟'));
    for s in SUFFIX.iter().filter(|_| suffix_first) {
        if let Some(n) = match_str(t, p, s) {
            sfx[k] = (p + n, Some(s));
            k += 1;
        }
    }
    for &(se, s) in &sfx[..k] {
        if t.get(se) == Some(&'分') && t.get(se + 1) == Some(&'之') {
            for ne in card_ends(t, se + 2) {
                out.push((ne, [s.map(|s| ("denominator", Lit(s))), Some(("numerator", Card(se + 2, ne))), None]));
            }
        }
    }
    // graph_hundred: 100%
    if match_str(t, p, "100%").is_some() {
        out.push((p + 4, [Some(("numerator", Lit("百"))), Some(("denominator", Lit("百"))), None]));
    }
}

/// `graph_decimal + % + denominator: "百"` (the integer part is `closure(card . 点 digits)`, possibly empty): every
/// `%` end with its value as a chain of `(card start, card end, fraction end)` pieces.
fn decimal_percent(t: &[char], p: usize, mut f: impl FnMut(usize, &Buf<Piece, 8>, u32)) {
    // reachable (pos, chain) after k ≥ 0 repetitions of `card . digits+`; one value per position (longer
    // fractional parts first: `1.523.4%` → `一点五二三点四`, the oracle's pick)
    let mut frontier: Buf<Piece, 8> = Buf::new();
    frontier.push(Piece { parent: ROOT, a: p, ce: p, e: p });
    let mut seen = 0;
    while seen < frontier.len() {
        let cur = frontier.get(seen);
        let pos = cur.e;
        let id = seen as u32;
        seen += 1;
        if t.get(pos) == Some(&'%') {
            f(pos + 1, &frontier, id);
        }
        for ce in card_ends(t, pos) {
            if t.get(ce) != Some(&'.') {
                continue;
            }
            let de = digit_end(t, ce + 1);
            for e in (ce + 2..=de).rev() {
                if (0..frontier.len()).any(|k| frontier.get(k).e == e) {
                    continue;
                }
                frontier.push(Piece { parent: id, a: pos, ce, e });
            }
        }
    }
}

/// One `card . digits` repetition of [`decimal_percent`] (the root has `a == e`).
#[derive(Clone, Copy, Default)]
struct Piece {
    parent: u32,
    a: usize,
    ce: usize,
    e: usize,
}

fn write_piece(t: &[char], pieces: &Buf<Piece, 8>, i: u32, out: &mut String) {
    let pc = pieces.get(i as usize);
    if pc.parent == ROOT {
        return;
    }
    write_piece(t, pieces, pc.parent, out);
    card(t, pc.a, pc.ce, out);
    out.push('点');
    digit_names(&t[pc.ce + 1..pc.e], out);
}

/// Whether an unsigned digit-first body can continue its digit run with `f`: 又 / 分(之) % or the decimal point.
pub fn digit_follower(f: char) -> bool {
    matches!(f, '又' | '/' | '分' | '%' | '.')
}

pub struct Fraction;

impl Gen for Fraction {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if !matches!(
            c,
            '0'..='9'
                | '-'
                | '负'
                | '負'
                | '%'
                | '分'
                | '百'
                | '千'
                | '万'
                | '十'
                | '亿'
                | '萬'
                | '億'
                | '拾'
                | '佰'
                | '仟'
        ) {
            return;
        }
        // an unsigned digit-first body continues its digit run with 又 / 分之 % or the decimal point
        if c.is_ascii_digit() && !after_signed_digits(t, start).is_some_and(digit_follower) {
            return;
        }
        // graph_optional_sign: k ≥ 0 `-` (each `negative: "负"`), or one `负`/`負`
        let dashes = t[start..].iter().take_while(|&&c| c == '-').count();
        let fu = matches!(c, '负' | '負');
        let mut bs = Vec::new();
        for k in 0..=dashes + usize::from(fu) {
            let (p, n) = if k <= dashes { (start + k, k) } else { (start + 1, 1) };
            let sign = move |s: &mut String| {
                for _ in 0..n {
                    s.push_str("negative: \"负\"");
                }
                s.push(' ');
            };
            bs.clear();
            bodies(t, p, &mut bs);
            for b in &bs {
                sink.push(b.0, cost(-3.0), 0, "fraction", |s| {
                    sign(s);
                    write_body(t, b, s);
                });
            }
            // every `%` the decimal closure reaches ends the `[0-9.]*` run at `p`
            let mut r = p;
            while r < t.len() && (t[r].is_ascii_digit() || t[r] == '.') {
                r += 1;
            }
            if t.get(r) == Some(&'%') {
                decimal_percent(t, p, |e, pieces, id| {
                    sink.push(e, cost(-3.0), 0, "fraction", |s| {
                        sign(s);
                        s.push_str("integer_part: \"");
                        write_piece(t, pieces, id, s);
                        s.push_str("\" denominator: \"百\"");
                    });
                });
            }
        }
    }
}
