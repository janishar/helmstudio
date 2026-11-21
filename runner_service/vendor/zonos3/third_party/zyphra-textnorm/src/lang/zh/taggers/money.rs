//! `zh/taggers/money.py`: `currency integer_part` | `(integer_part unit [currency])` ×1-3 | `integer_part currency`
//! (mandarin, −3.0) | decimal amounts with a mandarin or symbol currency (−1.0).
//!
//! `integer_part` of the decimal branches is `closure(cardinal, 1) . 点 digits`: any split of the integer digits into
//! cardinals at the same cost. The whole-number reading is emitted (the oracle's arc order often picks a split such
//! as `十零点五` for `100.5`; those calls are listed ties, reason "number").

use std::sync::LazyLock;

use super::{
    Buf, Gen, LOSES, ROOT, Sink, card, card_ends, card_run, cardinals, digit_end, digit_names, field, first_chars,
    quantities,
};
use crate::text::{Table, cost, match_str};

crate::tsv!(CURRENCY, "zh/money/currency_major.tsv");
crate::tsv!(CURRENCY_MANDARIN, "zh/money/currency_mandarin.tsv");

/// `currency_major.tsv` keys with several names: the one the oracle's shortest path takes (`¥5` → 圆, `MNT5` →
/// 蒙古图格里克, …; queried with `triage.py --raw`). No grammar-level preference exists between them.
const PICK: &[(&str, &str)] = &[
    ("MNT", "蒙古图格里克"),
    ("¥", "圆"),
    ("₽", "卢布"),
    ("₪", "以色列谢克尔"),
    ("₱", "菲律宾披索"),
    ("L", "罗马尼亚列伊"),
    ("лв", "哈萨克斯坦腾格"),
    ("₨", "毛里求斯卢比"),
];

static CURRENCIES: LazyLock<Table> = LazyLock::new(|| {
    let mut rows = Vec::new();
    for r in CURRENCY.rows() {
        let keep = match PICK.iter().find(|(k, _)| *k == r.key) {
            Some((_, v)) => *v == r.value,
            None => true,
        };
        if keep {
            rows.push(r.clone());
        }
    }
    Table::from_rows(rows)
});

static CURRENCY_FIRST: LazyLock<Vec<char>> = LazyLock::new(|| first_chars(&CURRENCIES));

/// Whether a digit-first money span can continue its digit run with `f` (a quantity, a unit, a mandarin currency
/// or the decimal point).
pub fn digit_follower(f: char) -> bool {
    matches!(
        f,
        '.' | '万'
            | '十'
            | '百'
            | '千'
            | '亿'
            | '萬'
            | '億'
            | '拾'
            | '佰'
            | '仟'
            | '块'
            | '元'
            | '塊'
            | '角'
            | '毛'
            | '分'
    ) || MANDARIN_FIRST.binary_search(&f).is_ok()
}

/// Symbol currencies at `p`: (end, name).
fn currency<'a>(t: &'a [char], p: usize) -> impl Iterator<Item = (usize, &'a str)> + 'a {
    CURRENCIES.prefix_matches(t, p).map(|(r, e)| (e, r.value.as_str()))
}

static MANDARIN_FIRST: LazyLock<Vec<char>> = LazyLock::new(|| first_chars(&CURRENCY_MANDARIN));

fn mandarin<'a>(t: &'a [char], p: usize) -> impl Iterator<Item = (usize, &'a str)> + 'a {
    let any = t.get(p).is_some_and(|c| MANDARIN_FIRST.binary_search(c).is_ok());
    any.then(|| CURRENCY_MANDARIN.prefix_matches(t, p).map(|(r, e)| (e, r.value.as_str()))).into_iter().flatten()
}

/// A `number_component` reading: `cardinal` over `t[p..card_end]`, optionally followed by a quantity word; ends at
/// `end`.
#[derive(Clone, Copy, Default)]
struct Num {
    p: usize,
    card_end: usize,
    quantity: Option<&'static str>,
    end: usize,
}

impl Num {
    fn write(&self, t: &[char], out: &mut String) {
        out.push_str("integer_part: \"");
        card(t, self.p, self.card_end, out);
        if let Some(q) = self.quantity {
            out.push_str(q);
        }
        out.push('"');
    }
}

/// `number_component` readings at `p` (`cardinal | cardinal + suffix`), in union order.
fn numbers(t: &[char], p: usize, mut f: impl FnMut(Num)) {
    for e in card_ends(t, p) {
        let n = Num { p, card_end: e, quantity: None, end: e };
        quantities(t, e, |qe, q| f(Num { quantity: Some(q), end: qe, ..n }));
        f(n);
    }
}

/// The [`numbers`] readings a unit or mandarin currency can follow: units, currencies and quantities never start
/// with a digit, so only the cardinal over the whole digit run.
fn run_numbers(t: &[char], p: usize, mut f: impl FnMut(Num)) {
    if let Some(e) = card_run(t, p) {
        let n = Num { p, card_end: e, quantity: None, end: e };
        quantities(t, e, |qe, q| f(Num { quantity: Some(q), end: qe, ..n }));
        f(n);
    }
}

/// One `unit_components` reading: `key: "<word × count>"`.
#[derive(Clone, Copy, Default)]
struct Unit {
    end: usize,
    key: &'static str,
    word: &'static str,
    count: usize,
}

/// `unit_components` at `p`, in union order.
fn units(t: &[char], p: usize, mut f: impl FnMut(Unit)) {
    if !matches!(t.get(p), Some('块' | '元' | '塊' | '角' | '毛' | '分')) {
        return;
    }
    for w in ["块", "元", "块钱"] {
        if let Some(n) = match_str(t, p, w) {
            f(Unit { end: p + n, key: "currency", word: w, count: 1 });
        }
    }
    for (w, word) in [("塊", "块"), ("塊錢", "块钱")] {
        let mut e = p;
        let mut count = 0;
        while let Some(n) = match_str(t, e, w) {
            e += n;
            count += 1;
            f(Unit { end: e, key: "currency", word, count });
        }
    }
    for (w, key, word) in [('角', "currency_maj", "角"), ('毛', "currency_maj", "毛"), ('分', "currency_min", "分")]
    {
        if t.get(p) == Some(&w) {
            f(Unit { end: p + 1, key, word, count: 1 });
        }
    }
}

/// One link of a chain of repeated components (each closure below concatenates its repetitions' fields without a
/// separator).
#[derive(Clone, Copy, Default)]
enum Piece<'a> {
    #[default]
    Root,
    /// `graph_unit_only`: number, unit, optional mandarin currency.
    Unit { num: Num, unit: Unit, mandarin: Option<&'a str> },
    /// `graph_decimal`: `integer_part: "<cardinals over t[p..ie]>点<digits t[ie + 1..e]>"`.
    Dec { p: usize, ie: usize, e: usize },
    /// `currency: "<name>"` (symbol or mandarin currency closure).
    Currency(&'a str),
}

#[derive(Clone, Copy, Default)]
struct Node<'a> {
    parent: u32,
    piece: Piece<'a>,
    end: usize,
    /// Some link is `currency_min` (the lattice's tie rank, `zh::LOSES`).
    fen: bool,
}

type Arena<'a> = Buf<Node<'a>, 6>;

fn add<'a>(arena: &mut Arena<'a>, parent: u32, piece: Piece<'a>, end: usize) -> u32 {
    let fen = matches!(piece, Piece::Unit { unit: Unit { key: "currency_min", .. }, .. })
        || (parent != ROOT && arena.get(parent as usize).fen);
    arena.push(Node { parent, piece, end, fen }) as u32
}

fn write_chain(t: &[char], arena: &Arena, i: u32, out: &mut String) {
    let node = arena.get(i as usize);
    if node.parent != ROOT {
        write_chain(t, arena, node.parent, out);
    }
    match node.piece {
        Piece::Root => {}
        Piece::Unit { num, unit, mandarin } => {
            num.write(t, out);
            out.push(' ');
            out.push_str(unit.key);
            out.push_str(": \"");
            for _ in 0..unit.count {
                out.push_str(unit.word);
            }
            out.push('"');
            if let Some(m) = mandarin {
                out.push(' ');
                field(out, "currency", m);
            }
        }
        Piece::Dec { p, ie, e } => {
            out.push_str("integer_part: \"");
            cardinals(&t[p..ie], out);
            out.push('点');
            digit_names(&t[ie + 1..e], out);
            out.push('"');
        }
        Piece::Currency(name) => field(out, "currency", name),
    }
}

/// `closure(graph_decimal, 1)` at `p`: the chain ids in the order the repetitions are found (depth first, last
/// found first).
fn decimals<'a>(t: &[char], p: usize, arena: &mut Arena<'a>, all: &mut Buf<u32, 8>) {
    if t.get(digit_end(t, p)) != Some(&'.') {
        return;
    }
    let mut frontier: Buf<(usize, u32), 8> = Buf::new();
    frontier.push((p, ROOT));
    while let Some((pos, parent)) = frontier.pop() {
        // graph_decimal: cardinals . 点 digits
        let ie = digit_end(t, pos);
        if ie == pos || t.get(ie) != Some(&'.') {
            continue;
        }
        for e in ie + 2..=digit_end(t, ie + 1) {
            let id = add(arena, parent, Piece::Dec { p: pos, ie, e }, e);
            all.push(id);
            frontier.push((e, id));
        }
    }
}

pub struct Money;

impl Gen for Money {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if c.is_ascii_digit() {
            // every digit-first branch continues the whole digit run with a quantity, unit, mandarin currency or `.`
            let r = digit_end(t, start);
            let Some(&f) = t.get(r) else { return };
            // a mandarin first char only counts when a whole currency name follows
            let mandarin_only = !matches!(
                f,
                '.' | '万'
                    | '十'
                    | '百'
                    | '千'
                    | '亿'
                    | '萬'
                    | '億'
                    | '拾'
                    | '佰'
                    | '仟'
                    | '块'
                    | '元'
                    | '塊'
                    | '角'
                    | '毛'
                    | '分'
            );
            if !digit_follower(f) || (mandarin_only && mandarin(t, r).next().is_none()) {
                return;
            }
            let mut arena = Arena::new();
            // graph_units and graph_mandarin_money read a quantity, unit or mandarin currency right after the run;
            // only graph_decimal_money continues with `.`
            let decimal_only = f == '.';
            // graph_units: closure(graph_unit_only, 1, 3)
            let mut frontier: Buf<(usize, u32, u8), 8> = Buf::new();
            if !decimal_only {
                frontier.push((start, ROOT, 0));
            }
            while let Some((pos, parent, k)) = frontier.pop() {
                if k == 3 {
                    continue;
                }
                let mut nums: Buf<Num, 4> = Buf::new();
                run_numbers(t, pos, |n| {
                    nums.push(n);
                });
                for ni in 0..nums.len() {
                    let num = nums.get(ni);
                    units(t, num.end, |unit| {
                        for (me, m) in mandarin(t, unit.end) {
                            let id = add(&mut arena, parent, Piece::Unit { num, unit, mandarin: Some(m) }, me);
                            let rank = if arena.get(id as usize).fen { LOSES } else { 0 };
                            sink.push(me, 0, rank, "money", |s| write_chain(t, &arena, id, s));
                            frontier.push((me, id, k + 1));
                        }
                        let id = add(&mut arena, parent, Piece::Unit { num, unit, mandarin: None }, unit.end);
                        let rank = if arena.get(id as usize).fen { LOSES } else { 0 };
                        sink.push(unit.end, 0, rank, "money", |s| write_chain(t, &arena, id, s));
                        frontier.push((unit.end, id, k + 1));
                    });
                }
            }
            // graph_mandarin_money (−3.0)
            if !decimal_only {
                run_numbers(t, start, |n| {
                    for (me, m) in mandarin(t, n.end) {
                        sink.push(me, cost(-3.0), 0, "money", |s| {
                            n.write(t, s);
                            s.push(' ');
                            field(s, "currency", m);
                        });
                    }
                });
            }
            // graph_decimal_money, first branch (−1.0)
            let mut all: Buf<u32, 8> = Buf::new();
            decimals(t, start, &mut arena, &mut all);
            for di in 0..all.len() {
                let d = all.get(di);
                let de = arena.get(d as usize).end;
                let mut tails: Buf<(usize, Option<&'static str>), 4> = Buf::new();
                tails.push((de, None));
                quantities(t, de, |qe, q| {
                    tails.push((qe, Some(q)));
                });
                for ti in 0..tails.len() {
                    let (te, q) = tails.get(ti);
                    // closure(currency_mandarin_component, 1)
                    let mut frontier: Buf<(usize, u32), 8> = Buf::new();
                    frontier.push((te, ROOT));
                    while let Some((pos, parent)) = frontier.pop() {
                        for (me, m) in mandarin(t, pos) {
                            let id = add(&mut arena, parent, Piece::Currency(m), me);
                            sink.push(me, cost(-1.0), 0, "money", |s| {
                                write_chain(t, &arena, d, s);
                                if let Some(q) = q {
                                    s.push_str(" quantity: \"");
                                    s.push_str(q);
                                    s.push('"');
                                }
                                s.push(' ');
                                write_chain(t, &arena, id, s);
                            });
                            frontier.push((me, id));
                        }
                    }
                }
            }
            return;
        }
        // currency first: graph_regular_money (0) and the decimal branch's closure(currency, 1) (−1.0)
        if CURRENCY_FIRST.binary_search(&c).is_err() {
            return;
        }
        let mut arena = Arena::new();
        let mut curs: Buf<u32, 8> = Buf::new();
        let mut frontier: Buf<(usize, u32), 8> = Buf::new();
        frontier.push((start, ROOT));
        while let Some((pos, parent)) = frontier.pop() {
            for (ce, name) in currency(t, pos) {
                let id = add(&mut arena, parent, Piece::Currency(name), ce);
                curs.push(id);
                frontier.push((ce, id));
            }
        }
        for ci in 0..curs.len() {
            let cur = curs.get(ci);
            let node = arena.get(cur as usize);
            let ce = node.end;
            if node.parent == ROOT {
                numbers(t, ce, |n| {
                    sink.push(n.end, 0, 0, "money", |s| {
                        write_chain(t, &arena, cur, s);
                        s.push(' ');
                        n.write(t, s);
                    });
                });
            }
            let mut all: Buf<u32, 8> = Buf::new();
            decimals(t, ce, &mut arena, &mut all);
            for di in 0..all.len() {
                let d = all.get(di);
                let de = arena.get(d as usize).end;
                let head = |s: &mut String| {
                    write_chain(t, &arena, cur, s);
                    s.push(' ');
                    write_chain(t, &arena, d, s);
                };
                quantities(t, de, |qe, q| {
                    sink.push(qe, cost(-1.0), 0, "money", |s| {
                        head(s);
                        s.push(' ');
                        field(s, "quantity", q);
                    });
                });
                sink.push(de, cost(-1.0), 0, "money", head);
            }
        }
    }
}
