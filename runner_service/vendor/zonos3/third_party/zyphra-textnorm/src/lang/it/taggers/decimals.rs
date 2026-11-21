//! `it/taggers/decimals.py` (deterministic). Pieces are shared with measure and money, which embed
//! `final_graph_wo_negative`.

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_nemo_space};

use super::cardinal::{self, Num, WORDS};

crate::tsv!(QUANTITIES, "it/numbers/quantities.tsv");

const W: Cost = cost(0.00001);

fn push_word(out: &mut String, w: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(w);
}

fn single(c: u8, out: &mut String) {
    if c == b'0' { push_word(out, &WORDS.zero) } else { push_word(out, &WORDS.digit[(c - b'0') as usize]) }
}

fn tens(t: u8, u: u8, out: &mut String, c: &mut Cost) -> bool {
    if t == b'0' {
        return false;
    }
    if !out.is_empty() {
        out.push(' ');
    }
    cardinal::graph_tens(t, u, out, c)
}

fn zeros(s: &[u8]) -> bool {
    s.iter().all(|&b| b == b'0')
}

/// `self.graph` on the fractional digits (whitespace already removed): the cheapest reading.
pub fn fractional(s: &[u8]) -> Option<(String, Cost)> {
    if s.is_empty() {
        return None;
    }
    let mut best: Option<(String, Cost)> = None;
    let mut offer = |o: String, c: Cost| {
        if best.as_ref().is_none_or(|(_, b)| c < *b) {
            best = Some((o, c));
        }
    };
    let zero_tail = |o: &mut String, rest: &[u8]| {
        for _ in rest {
            push_word(o, &WORDS.zero);
        }
    };
    // graph_digit + (" " zero)*  (-0.00001)
    if zeros(&s[1..]) {
        let mut o = String::new();
        single(s[0], &mut o);
        zero_tail(&mut o, &s[1..]);
        offer(o, -W);
    }
    // tens + (" " zero)*  (-0.00002)
    if s.len() >= 2 && zeros(&s[2..]) {
        let (mut o, mut c) = (String::new(), -2 * W);
        if tens(s[0], s[1], &mut o, &mut c) {
            zero_tail(&mut o, &s[2..]);
            offer(o, c);
        }
    }
    // hundreds + (" " zero)*  (+0.00001)
    if s.len() >= 3 && s[0] != b'0' && zeros(&s[3..]) {
        let (mut w, mut c) = (String::new(), W);
        if cardinal::hundreds(&s[..3], &mut w, &mut c) {
            let mut o = w;
            zero_tail(&mut o, &s[3..]);
            offer(o, c);
        }
    }
    // tens (" " tens)+ (" " zero)? ((" " digit)? | (" " zero)*)  (-0.00002)
    {
        let (mut o, mut c) = (String::new(), -2 * W);
        let mut i = 0;
        while i + 2 <= s.len() && tens(s[i], s[i + 1], &mut o, &mut c) {
            i += 2;
            if i >= 4 {
                let r = &s[i..];
                let ok = r.is_empty() || zeros(r) || r.len() == 1 || (r.len() == 2 && r[0] == b'0');
                if ok {
                    let mut oo = o.clone();
                    for &b in r {
                        single(b, &mut oo);
                    }
                    offer(oo, c);
                }
            }
        }
    }
    // zero (" " zero)* (" " graph_digit)*  (0)
    if s[0] == b'0' {
        let mut o = String::new();
        for &b in s {
            single(b, &mut o);
        }
        offer(o, 0);
    }
    best
}

/// The weight of [`fractional`]'s reading, without writing it.
pub fn fractional_cost(s: &[u8]) -> Option<Cost> {
    if s.is_empty() {
        return None;
    }
    let mut best: Option<Cost> = None;
    let mut offer = |c: Cost| {
        if best.is_none_or(|b| c < b) {
            best = Some(c);
        }
    };
    // graph_digit + (" " zero)*
    if zeros(&s[1..]) {
        offer(-W);
    }
    // tens + (" " zero)*
    if s.len() >= 2 && s[0] != b'0' && zeros(&s[2..]) {
        if let Some(tc) = cardinal::graph_tens_cost(s[0], s[1]) {
            offer(-2 * W + tc);
        }
    }
    // hundreds + (" " zero)*
    if s.len() >= 3 && s[0] != b'0' && zeros(&s[3..]) {
        let tc = if s[1] == b'0' { Some(0) } else { cardinal::graph_tens_cost(s[1], s[2]) };
        if let Some(tc) = tc {
            offer(W + tc);
        }
    }
    // tens (" " tens)+ (" " zero)? ((" " digit)? | (" " zero)*)
    {
        let mut c = -2 * W;
        let mut i = 0;
        while i + 2 <= s.len() && s[i] != b'0' {
            let Some(tc) = cardinal::graph_tens_cost(s[i], s[i + 1]) else { break };
            c += tc;
            i += 2;
            if i >= 4 {
                let r = &s[i..];
                if r.is_empty() || zeros(r) || r.len() == 1 || (r.len() == 2 && r[0] == b'0') {
                    offer(c);
                }
            }
        }
    }
    // zero (" " zero)* (" " graph_digit)*
    if s[0] == b'0' {
        offer(0);
    }
    best
}

/// A fractional span: digits and whitespace anywhere (deleted by `strip_formatting`, so trailing whitespace may
/// be absorbed: oracle `₽21,82 .` → money + `.` glued), holding at least one digit.
pub fn fractional_spans(text: &[char], start: usize, mut f: impl FnMut(usize, &[u8])) {
    let mut digits: Vec<u8> = Vec::new();
    let mut p = start;
    while p < text.len() {
        let c = text[p];
        if c.is_ascii_digit() {
            digits.push(c as u8);
            p += 1;
            f(p, &digits);
        } else if is_nemo_space(c) {
            p += 1;
            if !digits.is_empty() {
                f(p, &digits);
            }
        } else {
            break;
        }
    }
}

/// One reading of `final_graph_wo_negative` (decimal or quantity form) over `text[start..end]`; the fields are
/// written on demand ([`Dec::fields`]).
#[derive(Clone, Copy)]
pub struct Dec {
    pub end: usize,
    pub cost: Cost,
    /// The integer digits.
    int: Num,
    /// `strip_cardinal_apocope` on the integer words (decimal form; the quantity-only form reads them plain).
    apocope: bool,
    /// The fractional span `text[a..b]` (digits and whitespace).
    frac: Option<(usize, usize)>,
    /// The quantity key `text[a..b]`.
    quantity: Option<(usize, usize)>,
}

impl Dec {
    pub fn has_quantity(&self) -> bool {
        self.quantity.is_some()
    }

    /// The fields as the tagger writes them (`integer_part: "…" fractional_part: "…"` …).
    pub fn fields(&self, text: &[char], out: &mut String) {
        out.push_str("integer_part: \"");
        let head = out.len();
        cardinal::words_into(self.int.digits(), out);
        if self.apocope {
            cardinal::strip_apocope_from(out, head);
        }
        out.push('"');
        if let Some((a, b)) = self.frac {
            let digits: Vec<u8> = text[a..b].iter().filter(|c| c.is_ascii_digit()).map(|&c| c as u8).collect();
            out.push_str(" fractional_part: \"");
            out.push_str(&fractional(&digits).expect("fractional reading").0);
            out.push('"');
        }
        if let Some((a, b)) = self.quantity {
            let row = QUANTITIES.prefix_matches(text, a).find(|&(_, e)| e == b).expect("quantity key").0;
            out.push_str(" quantity: \"");
            out.push_str(&row.value);
            out.push('"');
        }
    }
}

/// `strip_cardinal_apocope(cardinal.graph)` on the integer spans at `start` followed by `,`: `f(span, cost)`.
fn integers(text: &[char], start: usize, mut f: impl FnMut(&Num, Cost)) {
    cardinal::spans(text, start, |n| {
        if text.get(n.end) != Some(&',') {
            return;
        }
        if let Some(c) = cardinal::cost_of(n.digits()) {
            f(n, c);
        }
    });
}

/// The quantity key ends after a literal space at `p`.
fn quantity_at(text: &[char], p: usize, mut f: impl FnMut(usize)) {
    if text.get(p) != Some(&' ') {
        return;
    }
    for (_, end) in QUANTITIES.prefix_matches(text, p + 1) {
        f(end);
    }
}

thread_local! {
    /// `wo_negative` results of the current input, per start (decimal, money and measure each ask for them).
    static CACHE: std::cell::RefCell<Vec<Option<std::rc::Rc<[Dec]>>>> = const { std::cell::RefCell::new(Vec::new()) };
    /// The shared "no reading" result.
    static EMPTY: std::rc::Rc<[Dec]> = std::rc::Rc::new([]);
}

/// Start of a new input: the lattice queries position 0 first, and the classify union calls this there.
pub fn reset(len: usize) {
    CACHE.with_borrow_mut(|slots| {
        slots.clear();
        slots.resize(len + 1, None);
    });
}

/// Whether `wo_negative` can read anything at `start`: an integer span followed by `,`, or digits (with
/// separators) then a space and a quantity word.
fn may_read(text: &[char], start: usize) -> bool {
    let mut p = start;
    let mut digit = false;
    while p < text.len() {
        match text[p] {
            c if c.is_ascii_digit() => digit = true,
            '.' => {}
            ' ' => {
                if digit && QUANTITIES.prefix_matches(text, p + 1).next().is_some() {
                    return true;
                }
            }
            ',' => return digit,
            _ => return false,
        }
        p += 1;
    }
    false
}

/// [`wo_negative`] memoized per input text and start.
pub fn readings(text: &[char], start: usize) -> std::rc::Rc<[Dec]> {
    if !text.get(start).is_some_and(|c| c.is_ascii_digit() || *c == '.') || !may_read(text, start) {
        return EMPTY.with(std::rc::Rc::clone);
    }
    CACHE.with_borrow_mut(|slots| {
        if slots.len() != text.len() + 1 {
            slots.clear();
            slots.resize(text.len() + 1, None);
        }
        slots[start]
            .get_or_insert_with(|| {
                let mut v = Vec::new();
                wo_negative(text, start, &mut v);
                v.into()
            })
            .clone()
    })
}

/// `final_graph_wo_negative`: every span at `start`.
fn wo_negative(text: &[char], start: usize, out: &mut Vec<Dec>) {
    // the quantity form deletes separators anywhere, so it may start at one (`£6.526.967 miliardi`)
    if !text.get(start).is_some_and(|c| c.is_ascii_digit() || *c == '.') {
        return;
    }
    let first = out.len();
    integers(text, start, |n, ic| {
        let fs = n.end + 1;
        fractional_spans(text, fs, |end, digits| {
            let Some(fc) = fractional_cost(digits) else { return };
            out.push(Dec { end, cost: ic + fc, int: *n, apocope: true, frac: Some((fs, end)), quantity: None });
        });
    });
    // final_graph_wo_sign + " " + quantity
    let n = out.len();
    for i in first..n {
        let d = out[i];
        quantity_at(text, d.end, |qend| {
            out.push(Dec { end: qend, quantity: Some((d.end + 1, qend)), ..d });
        });
    }
    // closure(digit, 1, 6) @ cardinal.graph after deleting separators, " ", quantity
    let mut num = Num { end: start, len: 0, buf: [0; 24] };
    let mut p = start;
    while p < text.len() {
        let c = text[p];
        if c.is_ascii_digit() {
            if num.len == 6 {
                break;
            }
            num.buf[num.len] = c as u8;
            num.len += 1;
        } else if !matches!(c, '.' | ' ') {
            break;
        }
        p += 1;
        // a space here may be the literal NEMO_SPACE before the quantity
        if c == ' ' && num.len > 0 && QUANTITIES.prefix_matches(text, p).next().is_some() {
            if let Some(ic) = cardinal::cost_of(num.digits()) {
                quantity_at(text, p - 1, |qend| {
                    out.push(Dec {
                        end: qend,
                        cost: ic,
                        int: num,
                        apocope: false,
                        frac: None,
                        quantity: Some((p, qend)),
                    });
                });
            }
        }
    }
}

pub struct Decimal {
    pub cost: Cost,
}

impl Classifier for Decimal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let neg = text[start] == '-';
        let p = start + neg as usize;
        if p >= text.len() {
            return;
        }
        for d in readings(text, p).iter() {
            let tag = super::tag(d.end, 96, |t| {
                t.push_str("decimal { ");
                if neg {
                    t.push_str("negative: \"true\" ");
                }
                d.fields(text, t);
                t.push_str(" preserve_order: true }");
            });
            out.push(Candidate { end: d.end, cost: self.cost + d.cost, rank: super::sign_rank(neg), tag });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn f(s: &str) -> String {
        fractional(s.as_bytes()).unwrap().0
    }

    #[test]
    fn fractional_readings() {
        // oracle: 1,850 / 1,8055 / 1,23405 / 1,123 / 1,1234 / 1,505 / 1,05 / 1,100
        assert_eq!(f("850"), "ottantacinque zero");
        assert_eq!(f("8055"), "ottanta cinquantacinque");
        assert_eq!(f("23405"), "ventitre quaranta cinque");
        assert_eq!(f("123"), "cento ventitre");
        assert_eq!(f("1234"), "dodici trentaquattro");
        assert_eq!(f("505"), "cinquecento cinque");
        assert_eq!(f("05"), "zero cinque");
        assert_eq!(f("100"), "dieci zero");
        assert!(fractional(b"8505").is_none());
    }

    #[test]
    fn fractional_cost_matches_fractional() {
        let mut s = Vec::new();
        for len in 1..=6u32 {
            for n in 0..10u32.pow(len) {
                s.clear();
                s.extend(format!("{n:0width$}", width = len as usize).bytes());
                assert_eq!(fractional_cost(&s), fractional(&s).map(|x| x.1), "{n:0width$}", width = len as usize);
            }
        }
    }
}
