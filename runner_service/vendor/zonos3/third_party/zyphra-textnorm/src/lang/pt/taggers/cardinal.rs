//! `pt/taggers/cardinal.py`. The upstream graph is functional per input except for the union of
//! `pad @ graph_24 @ norm` (cost 0, −0.001 per scale block equal to `000001`) with the bilhão/trilhão
//! `_build_magnitude_pattern`s (−1.0); [`Num::value`] returns the cheapest output, which is the only one any
//! consumer of `cardinal.graph` can surface (no pt consumer filters a 10+ digit cardinal's output).

use std::sync::LazyLock;

use crate::text::{Cost, cost, is_digit};

crate::tsv!(DIGIT, "pt/numbers/digit.tsv");
crate::tsv!(ZERO, "pt/numbers/zero.tsv");
crate::tsv!(TEENS, "pt/numbers/teens.tsv");
crate::tsv!(TENS, "pt/numbers/tens.tsv");
crate::tsv!(HUNDREDS, "pt/numbers/hundreds.tsv");
crate::labels!(SPECIALS, "pt/numbers/cardinal_specials.tsv");
crate::labels!(SCALES, "pt/numbers/scales.tsv");

pub struct Scale {
    pub one: String,
    pub plural: String,
    pub zeros: usize,
}

/// The number words of `cardinal.py`, indexed by digit value.
pub struct Num {
    pub digit: [String; 10],
    pub teen: [String; 10],
    pub tens: [String; 10],
    pub hundreds: [String; 10],
    pub zero: String,
    pub conn: String,
    pub thousand: String,
    pub hundred_100: String,
    pub hundred_1: String,
    pub scales: Vec<Scale>,
}

fn lookup(t: &crate::text::Table, k: &str) -> String {
    t.exact(k).next().map(|r| r.value.clone()).unwrap_or_default()
}

pub static NUM: LazyLock<Num> = LazyLock::new(|| {
    let mut n = Num {
        digit: Default::default(),
        teen: Default::default(),
        tens: Default::default(),
        hundreds: Default::default(),
        zero: lookup(&ZERO, "0"),
        conn: String::new(),
        thousand: String::new(),
        hundred_100: String::new(),
        hundred_1: String::new(),
        scales: Vec::new(),
    };
    for d in 0..10u8 {
        let k = d.to_string();
        n.digit[d as usize] = lookup(&DIGIT, &k);
        n.teen[d as usize] = lookup(&TEENS, &format!("1{d}"));
        n.tens[d as usize] = lookup(&TENS, &k);
        n.hundreds[d as usize] = lookup(&HUNDREDS, &k);
    }
    let spec =
        |key: &str| SPECIALS.iter().find(|r| r.len() >= 2 && r[0] == key).map(|r| r[1].clone()).unwrap_or_default();
    n.conn = format!(" {} ", spec("connector"));
    n.thousand = spec("thousand");
    n.hundred_100 = spec("hundred_100");
    n.hundred_1 = spec("hundred_1");
    for r in SCALES.iter() {
        if r.len() >= 3 && !r[2].trim().is_empty() && r[2].trim().bytes().all(|b| b.is_ascii_digit()) {
            n.scales.push(Scale { one: r[0].clone(), plural: r[1].clone(), zeros: r[2].trim().parse().unwrap() });
        }
    }
    n
});

type D3 = [u8; 3];

const fn is_zero(s: &[u8]) -> bool {
    let mut i = 0;
    while i < s.len() {
        if s[i] != 0 {
            return false;
        }
        i += 1;
    }
    true
}

/// `10**k` zero-padded to `s.len()` digits.
fn is_pure(s: &[u8]) -> bool {
    s.iter().filter(|&&d| d != 0).count() == 1 && s.iter().any(|&d| d == 1)
}

impl Num {
    /// `graph_tens` on two digits.
    pub fn tens2(&self, a: u8, b: u8, o: &mut String) -> bool {
        match a {
            1 => o.push_str(&self.teen[b as usize]),
            2..=9 => {
                o.push_str(&self.tens[a as usize]);
                if b != 0 {
                    o.push_str(&self.conn);
                    o.push_str(&self.digit[b as usize]);
                }
            }
            _ => return false,
        }
        true
    }

    fn digit1(&self, d: u8, o: &mut String) -> bool {
        if d == 0 {
            return false;
        }
        o.push_str(&self.digit[d as usize]);
        true
    }

    /// `graph_hundreds` on exactly three digits.
    pub fn hundreds3(&self, s: D3, o: &mut String) -> bool {
        match s[0] {
            2..=9 => {
                o.push_str(&self.hundreds[s[0] as usize]);
                if s[1] == 0 && s[2] == 0 {
                    true
                } else if s[1] != 0 {
                    o.push_str(&self.conn);
                    self.tens2(s[1], s[2], o)
                } else {
                    o.push_str(&self.conn);
                    self.digit1(s[2], o)
                }
            }
            1 => {
                if s[1] == 0 && s[2] == 0 {
                    o.push_str(&self.hundred_100);
                    return true;
                }
                o.push_str(&self.hundred_1);
                o.push_str(&self.conn);
                if s[1] != 0 { self.tens2(s[1], s[2], o) } else { self.digit1(s[2], o) }
            }
            _ => false,
        }
    }

    /// `graph_hundreds` on two digits (`hundreds + connector_e + digit`).
    pub fn hundreds2(&self, a: u8, b: u8, o: &mut String) -> bool {
        if !(2..=9).contains(&a) || b == 0 {
            return false;
        }
        o.push_str(&self.hundreds[a as usize]);
        o.push_str(&self.conn);
        o.push_str(&self.digit[b as usize]);
        true
    }

    fn h_comp_base(&self, s: D3, o: &mut String) -> bool {
        if s[0] == 0 { self.tens2(s[1], s[2], o) } else { self.hundreds3(s, o) }
    }

    fn h_comp(&self, s: D3, o: &mut String) -> bool {
        if s[0] == 0 && s[1] == 0 { self.digit1(s[2], o) } else { self.h_comp_base(s, o) }
    }

    fn h_comp_no_one(&self, s: D3, o: &mut String) -> bool {
        if s[0] == 0 && s[1] == 0 { s[2] > 1 && self.digit1(s[2], o) } else { self.h_comp_base(s, o) }
    }

    fn pure_components(&self, s: D3, o: &mut String) -> bool {
        match s {
            [0, a, 0] if a != 0 => self.tens2(a, 0, o),
            [0, 0, d] if d != 0 => self.digit1(d, o),
            [1, 0, 0] => {
                o.push_str(&self.hundred_100);
                true
            }
            [h, 0, 0] if h >= 2 => {
                o.push_str(&self.hundreds[h as usize]);
                true
            }
            _ => false,
        }
    }

    fn compound_hundreds(&self, s: D3, o: &mut String) -> bool {
        match s[0] {
            1 => o.push_str(&self.hundred_1),
            2..=9 => o.push_str(&self.hundreds[s[0] as usize]),
            _ => return false,
        }
        o.push_str(&self.conn);
        if s[1] != 0 { self.tens2(s[1], s[2], o) } else { self.digit1(s[2], o) }
    }

    fn suffix_after_mil(&self, s: D3, o: &mut String) -> bool {
        if s == [0, 0, 0] {
            return true;
        }
        if (s[0] == 0 && (s[2] == 0 || s[1] == 0)) || (s[1] == 0 && s[2] == 0) {
            o.push_str(&self.conn);
            return self.pure_components(s, o);
        }
        if s[0] != 0 {
            o.push(' ');
            return self.compound_hundreds(s, o);
        }
        // "0" + graph_compound_tens: s[1], s[2] both non-zero here
        o.push_str(&self.conn);
        self.tens2(s[1], s[2], o)
    }

    fn t_comp(&self, a: D3, b: D3, o: &mut String) -> bool {
        match a {
            [0, 0, 0] => self.h_comp(b, o),
            [0, 0, 1] => {
                o.push_str(&self.thousand);
                self.suffix_after_mil(b, o)
            }
            _ => {
                if !self.h_comp_no_one(a, o) {
                    return false;
                }
                o.push(' ');
                o.push_str(&self.thousand);
                self.suffix_after_mil(b, o)
            }
        }
    }

    fn t_comp_no_one(&self, a: D3, b: D3, o: &mut String) -> bool {
        match a {
            [0, 0, 0] => return self.h_comp_no_one(b, o),
            [0, 0, 1] => o.push_str(&self.thousand),
            _ => {
                if !self.h_comp_no_one(a, o) {
                    return false;
                }
                o.push(' ');
                o.push_str(&self.thousand);
            }
        }
        if b == [0, 0, 0] {
            return true;
        }
        o.push(' ');
        self.h_comp(b, o)
    }

    fn u6_pure(&self, u: &[u8], o: &mut String) -> bool {
        match u {
            [0, 0, 0, 0, 0, 1] => self.digit1(1, o),
            [0, 0, 1, 0, 0, 0] => {
                o.push_str(&self.thousand);
                true
            }
            [0, 0, 0, 0, 1, 0] => self.tens2(1, 0, o),
            [0, 0, 0, 1, 0, 0] => {
                o.push_str(&self.hundred_100);
                true
            }
            [0, 1, 0, 0, 0, 0] => {
                self.tens2(1, 0, o);
                o.push(' ');
                o.push_str(&self.thousand);
                true
            }
            [1, 0, 0, 0, 0, 0] => {
                o.push_str(&self.hundred_100);
                o.push(' ');
                o.push_str(&self.thousand);
                true
            }
            _ => false,
        }
    }

    fn t_comp6(&self, u: &[u8], o: &mut String) -> bool {
        self.t_comp([u[0], u[1], u[2]], [u[3], u[4], u[5]], o)
    }

    /// `pad @ graph_24 @ norm` on a digit string without a leading zero, ≤ 24 digits.
    fn graph24(&self, digits: &[u8]) -> Option<(String, Cost)> {
        let mut d = [0u8; 24];
        d[24 - digits.len()..].copy_from_slice(digits);
        let mut raw = String::with_capacity(16 * digits.len() + 16);
        let mut c: Cost = 0;
        let u = &d[18..24];
        if is_zero(&d[..18]) {
            if is_pure(u) {
                self.u6_pure(u, &mut raw);
            } else if !self.t_comp6(u, &mut raw) {
                return None;
            }
        } else {
            for (i, scale) in self.scales.iter().rev().enumerate() {
                let g = &d[6 * i..6 * i + 6];
                if g == [0, 0, 0, 0, 0, 1] {
                    raw.push_str(&scale.one);
                    c += cost(-0.001);
                } else if !is_zero(g) {
                    if !self.t_comp_no_one([g[0], g[1], g[2]], [g[3], g[4], g[5]], &mut raw) {
                        return None;
                    }
                    raw.push_str(&scale.plural);
                }
                raw.push(' ');
            }
            if is_pure(u) {
                raw.push_str(&self.conn);
                self.u6_pure(u, &mut raw);
            } else if !is_zero(u) && !self.t_comp6(u, &mut raw) {
                return None;
            }
        }
        // `norm`: strip edge spaces, collapse inner runs (only scale blocks leave them)
        if !raw.starts_with(' ') && !raw.ends_with(' ') && !raw.contains("  ") {
            return Some((raw, c));
        }
        let mut out = String::with_capacity(raw.len());
        for w in raw.split([' ', '\t', '\n', '\r', '\u{a0}']).filter(|w| !w.is_empty()) {
            if !out.is_empty() {
                out.push(' ');
            }
            out.push_str(w);
        }
        Some((out, c))
    }

    fn scale3(&self, s: D3, scale: &Scale, o: &mut String) -> bool {
        match s {
            [0, 0, 1] => {
                o.push_str(&scale.one);
                true
            }
            [0, 0, 0] => true,
            _ => {
                if !self.h_comp_no_one(s, o) {
                    return false;
                }
                o.push(' ');
                o.push_str(&scale.plural);
                true
            }
        }
    }

    /// `_build_scale_trailing_graph(scale_3_mil, t_comp, 6, 9)`.
    fn graph9(&self, t: &[u8], o: &mut String) -> bool {
        let s = [t[0], t[1], t[2]];
        let r = &t[3..9];
        if s == [0, 0, 0] {
            return is_zero(r) || self.t_comp6(r, o);
        }
        if !self.scale3(s, &self.scales[0], o) {
            return false;
        }
        if is_zero(r) {
            return true;
        }
        o.push(' ');
        self.t_comp6(r, o)
    }

    /// `_build_scale_trailing_graph(scale_3_bi, graph_9, 9, 12)`.
    fn graph12(&self, t: &[u8], o: &mut String) -> bool {
        let s = [t[0], t[1], t[2]];
        let r = &t[3..12];
        if s == [0, 0, 0] {
            return self.graph9(r, o);
        }
        if !self.scale3(s, &self.scales[1], o) {
            return false;
        }
        if is_zero(r) {
            return true;
        }
        o.push(' ');
        self.graph9(r, o)
    }

    /// `_build_magnitude_pattern` for the scale with `zeros` trailing digits (−1.0 applied by the caller).
    fn magnitude(&self, digits: &[u8], scale: &Scale) -> Option<String> {
        let z = scale.zeros;
        let l = digits.len().checked_sub(z)?;
        if !(1..=3).contains(&l) || digits[0] == 0 {
            return None;
        }
        let mut o = String::new();
        let lead = &digits[..l];
        let ok = match l {
            1 if lead[0] == 1 => {
                o.push_str(&scale.one);
                true
            }
            1 => {
                o.push_str(&self.digit[lead[0] as usize]);
                o.push_str(&scale.plural);
                true
            }
            2 => {
                self.tens2(lead[0], lead[1], &mut o) && {
                    o.push_str(&scale.plural);
                    true
                }
            }
            _ => {
                self.hundreds3([lead[0], lead[1], lead[2]], &mut o) && {
                    o.push_str(&scale.plural);
                    true
                }
            }
        };
        if !ok {
            return None;
        }
        let t = &digits[l..];
        if is_zero(t) {
            return Some(o);
        }
        if is_pure(t) {
            o.push_str(&self.conn);
        } else {
            o.push(' ');
        }
        let ok = match z {
            9 => self.graph9(t, &mut o),
            12 => self.graph12(t, &mut o),
            _ => false,
        };
        ok.then_some(o)
    }

    /// `cardinal.graph` on a separator-free digit string (ASCII digit values 0-9): the cheapest output.
    pub fn value(&self, digits: &[u8]) -> Option<(String, Cost)> {
        if digits == [0] {
            return Some((self.zero.clone(), 0));
        }
        if digits.is_empty() || digits[0] == 0 {
            return None;
        }
        for scale in self.scales.iter().filter(|s| s.zeros > 0) {
            if let Some(o) = self.magnitude(digits, scale) {
                return Some((o, cost(-1.0)));
            }
        }
        if digits.len() > 24 {
            return None;
        }
        self.graph24(digits)
    }
}

/// Up to 32 digit values on the stack (longer inputs have no cardinal reading: > 24 digits and not a magnitude
/// pattern, whose longest form is 3 + 12 digits).
pub struct Digits {
    buf: [u8; 32],
    len: usize,
}

impl std::ops::Deref for Digits {
    type Target = [u8];
    fn deref(&self) -> &[u8] {
        &self.buf[..self.len]
    }
}

impl Digits {
    fn push(&mut self, c: char) -> bool {
        if self.len == 32 {
            return false;
        }
        self.buf[self.len] = c as u8 - b'0';
        self.len += 1;
        true
    }
}

/// Digits of `s` (chars) if all ASCII digits.
pub fn digits_of(s: &[char]) -> Option<Digits> {
    if s.is_empty() || s.len() > 32 || !s.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let mut d = Digits { buf: [0; 32], len: 0 };
    for &c in s {
        d.push(c);
    }
    Some(d)
}

/// `filter_cardinal_punctuation` input side: plain digits, or 1-3 digits then groups of three each preceded by
/// `.` or ` `. Returns the digits with separators deleted.
pub fn number_input(s: &[char]) -> Option<Digits> {
    let first = s.iter().position(|&c| !is_digit(c));
    let Some(first) = first else { return digits_of(s) };
    if !(1..=3).contains(&first) {
        return None;
    }
    let rest = &s[first..];
    if rest.len() % 4 != 0 {
        return None;
    }
    let mut out = digits_of(&s[..first])?;
    for g in rest.chunks(4) {
        if !(g[0] == '.' || g[0] == ' ') || !g[1..].iter().all(|&c| is_digit(c)) {
            return None;
        }
        for &c in &g[1..] {
            if !out.push(c) {
                return None;
            }
        }
    }
    Some(out)
}

/// `cardinal.graph` on a span: `(words, cost)`.
pub fn graph(s: &[char]) -> Option<(String, Cost)> {
    NUM.value(&number_input(s)?)
}

/// `cardinal.graph` on a span of digits only (`closure(NEMO_DIGIT) @ cardinal.graph`).
pub fn graph_digits(s: &[char]) -> Option<(String, Cost)> {
    NUM.value(&digits_of(s)?)
}

/// `cardinal { [negative: "true" ]integer: "…" }` (`CardinalFst.fst`).
pub fn tag(s: &[char]) -> Option<(Cost, String)> {
    let (neg, s) = match s.first() {
        Some('-') => (true, &s[1..]),
        _ => (false, s),
    };
    let (w, c) = graph(s)?;
    let mut t = String::with_capacity(w.len() + 48);
    t.push_str("cardinal { ");
    if neg {
        t.push_str("negative: \"true\" ");
    }
    t.push_str("integer: \"");
    t.push_str(&w);
    t.push_str("\" }");
    Some((c, t))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn w(s: &str) -> String {
        let c: Vec<char> = s.chars().collect();
        graph(&c).map(|(w, _)| w).unwrap_or_default()
    }

    #[test]
    fn golden_samples() {
        assert_eq!(w("1001001"), "um milhão mil e um");
        assert_eq!(w("1010101010"), "um bilhão dez  milhões cento e um mil e dez");
        assert_eq!(w("2000000001"), "dois bilhões e um");
        assert_eq!(w("2024"), "dois mil e vinte e quatro");
        assert_eq!(w("1 000 000"), "um milhão");
        assert_eq!(
            w("369395001630254"),
            "trezentos e sessenta e nove trilhões trezentos e noventa e cinco  bilhões um milhão seiscentos e trinta mil duzentos e cinquenta e quatro"
        );
    }
}
