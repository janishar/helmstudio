// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Russian money classifier, based on ru/taggers/money.py.
use super::cardinal::{number_graph, range_graph};
use super::decimal::{fractional_graph, integer_graph, render_integer_part};
use super::number::{Case, Gender, PluralCategory, cardinal, plural_category};
use super::units::{
    Form, MAJOR_EDGES, MAJOR_SUFFIXES, MINOR_EDGES, MINOR_WRITTEN_SUFFIXES, Noun, QUANTITY_NOUNS, QUANTITY_SUFFIXES,
    UNITS, agree,
};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

pub const MARK_ONE: char = '\u{e031}';
pub const MARK_FEW: char = '\u{e032}';
pub const MARK_MANY: char = '\u{e033}';
pub const MARK_GEN: char = '\u{e034}';
const RATES: &[(&str, &str)] = &[
    ("ч", "в час"),
    ("h", "в час"),
    ("hr", "в час"),
    ("час", "в час"),
    ("мин", "в минуту"),
    ("min", "в минуту"),
    ("с", "в секунду"),
    ("сек", "в секунду"),
    ("s", "в секунду"),
    ("sec", "в секунду"),
    ("д", "в день"),
    ("день", "в день"),
    ("сут", "в сутки"),
    ("сутки", "в сутки"),
    ("d", "в день"),
    ("day", "в день"),
    ("нед", "в неделю"),
    ("wk", "в неделю"),
    ("мес", "в месяц"),
    ("мес.", "в месяц"),
    ("mo", "в месяц"),
    ("год", "в год"),
    ("г", "в год"),
    ("yr", "в год"),
    ("чел", "на человека"),
    ("чел.", "на человека"),
    ("шт", "за штуку"),
    ("шт.", "за штуку"),
];
fn space(s: &str) -> &str {
    s.strip_suffix(' ').or_else(|| s.strip_suffix('\u{a0}')).unwrap_or(s)
}
fn weight(g: super::cardinal::NumberGraph) -> f64 {
    g.cost as f64 / 1_000_000.0
}
fn form(n: u64) -> Form {
    match plural_category(n) {
        PluralCategory::One => Form::One,
        PluralCategory::Few => Form::Few,
        PluralCategory::Many => Form::Many,
    }
}
fn marker(n: u64) -> char {
    match plural_category(n) {
        PluralCategory::One => MARK_ONE,
        PluralCategory::Few => MARK_FEW,
        PluralCategory::Many => MARK_MANY,
    }
}
fn counted(n: u64, noun: &Noun) -> String {
    agree(&cardinal(n, Gender::Masc, Case::Nom), form(n), noun)
}
fn gen_counted(n: u64, noun: &Noun) -> String {
    agree(
        &cardinal(n, Gender::Masc, Case::Gen),
        if plural_category(n) == PluralCategory::One { Form::Gen } else { Form::Many },
        noun,
    )
}
fn decimal(s: &str, point: bool) -> Option<(u64, super::decimal::FractionalGraph)> {
    let (a, b) = s.split_once(',').or_else(|| if point { s.split_once('.') } else { None })?;
    if s.contains('.') && (a.is_empty() || a.len() > 3 || !a.bytes().all(|b| b.is_ascii_digit())) {
        return None;
    }
    Some((integer_graph(a)?.value, fractional_graph(b)?))
}
fn render_decimal(d: (u64, super::decimal::FractionalGraph)) -> String {
    format!("{} {}", render_integer_part(d.0), d.1.render())
}
fn range_number(s: &str) -> Option<u64> {
    range_graph(s).map(|g| g.value)
}
enum Rate {
    Fixed(&'static str),
    Unit(&'static Noun),
}
impl Rate {
    fn render(&self) -> String {
        match self {
            Self::Fixed(s) => (*s).to_string(),
            Self::Unit(u) => format!("за {}", u.acc),
        }
    }
}
fn rate(s: &str) -> Option<(&str, Option<Rate>)> {
    if let Some((a, b)) = s.rsplit_once('/') {
        if let Some((_, v)) = RATES.iter().find(|(w, _)| *w == b) {
            return Some((a, Some(Rate::Fixed(v))));
        }
        let u = UNITS.iter().find(|u| u.written == b)?;
        Some((a, Some(Rate::Unit(u))))
    } else {
        Some((s, None))
    }
}
#[derive(Clone, Copy)]
enum AmountText {
    Number(u64),
    Decimal((u64, super::decimal::FractionalGraph)),
    Counted(u64, &'static Noun),
    DecimalQuantity((u64, super::decimal::FractionalGraph), &'static Noun),
}
impl AmountText {
    fn render(self) -> String {
        match self {
            Self::Number(n) => cardinal(n, Gender::Masc, Case::Nom),
            Self::Decimal(d) => render_decimal(d),
            Self::Counted(n, q) => counted(n, q),
            Self::DecimalQuantity(d, q) => format!("{} {}", render_decimal(d), q.genitive),
        }
    }
}
struct Amount {
    text: AmountText,
    form: Form,
    mark: char,
    weight: f64,
}
fn amounts(s: &str, mut emit: impl FnMut(Amount)) {
    if let Some(g) = number_graph(s, false) {
        emit(Amount {
            text: AmountText::Number(g.value),
            form: form(g.value),
            mark: marker(g.value),
            weight: weight(g),
        });
    }
    if let Some(d) = decimal(s, false) {
        emit(Amount { text: AmountText::Decimal(d), form: Form::Gen, mark: MARK_GEN, weight: 0.0 });
    }
    if let Some(base) = s.strip_suffix(".00").or_else(|| s.strip_suffix(",00")) {
        if let Some(g) = number_graph(base, false) {
            emit(Amount {
                text: AmountText::Number(g.value),
                form: form(g.value),
                mark: marker(g.value),
                weight: weight(g) - 0.05,
            });
        }
    }
    for &q in QUANTITY_SUFFIXES.matching(s) {
        if let Some(a) = s.strip_suffix(&q.written) {
            let a = space(a);
            if let Some(g) = number_graph(a, false) {
                emit(Amount {
                    text: AmountText::Counted(g.value, q),
                    form: Form::Many,
                    mark: MARK_MANY,
                    weight: weight(g),
                });
            }
            if let Some(d) = decimal(a, false) {
                emit(Amount {
                    text: AmountText::DecimalQuantity(d, q),
                    form: Form::Many,
                    mark: MARK_MANY,
                    weight: 0.0,
                });
            }
        }
    }
    for (sym, target) in
        [("k", "тыс"), ("K", "тыс"), ("к", "тыс"), ("К", "тыс"), ("M", "млн"), ("B", "млрд"), ("bn", "млрд")]
    {
        if let Some(a) = s.strip_suffix(sym) {
            if let Some(q) = QUANTITY_NOUNS.iter().find(|q| q.written == target) {
                let a = space(a);
                if let Some(g) = number_graph(a, false) {
                    emit(Amount {
                        text: AmountText::Counted(g.value, q),
                        form: Form::Many,
                        mark: MARK_MANY,
                        weight: weight(g) - 0.02,
                    });
                }
                if let Some(d) = decimal(a, true) {
                    emit(Amount {
                        text: AmountText::DecimalQuantity(d, q),
                        form: Form::Many,
                        mark: MARK_MANY,
                        weight: if a.contains('.') { 0.03 } else { -0.02 },
                    });
                }
            }
        }
    }
}
pub struct ParsedMoney {
    pub tag: String,
    pub cost: Cost,
}
fn parse_money(s: &str, target: Option<Cost>) -> Option<ParsedMoney> {
    let (core, rate) = rate(s)?;
    let rate_inline = || rate.as_ref().map(|r| format!(" {}", r.render())).unwrap_or_default();
    let rate_field =
        || rate.as_ref().map(|r| format!(" morphosyntactic_features: \"{}\"", r.render())).unwrap_or_default();
    let mut best: Option<ParsedMoney> = None;
    macro_rules! add {
        ($tag:expr, $weight:expr) => {{
            let c = cost(1.06 + $weight);
            if best.as_ref().map_or(true, |p| c < p.cost) {
                best = Some(ParsedMoney { tag: if Some(c) == target { $tag } else { String::new() }, cost: c });
            }
        }};
    }
    let (inline, field, unsigned) = if let Some(r) = core.strip_prefix('-') {
        ("минус ", "negative: \"минус\" ", r)
    } else if let Some(r) = core.strip_prefix('~') {
        ("около ", "negative: \"приблизительно\" ", r.strip_prefix(' ').unwrap_or(r))
    } else if let Some(r) = core.strip_prefix('±') {
        ("плюс-минус ", "negative: \"плюс-минус\" ", r.strip_prefix(' ').unwrap_or(r))
    } else {
        ("", "", core)
    };
    for curr in MAJOR_EDGES.matching(unsigned) {
        if let Some(a) = unsigned.strip_suffix(&curr.written) {
            amounts(space(a), |amount| {
                add!(
                    format!(
                        "integer_part: \"{inline}{}{}\"",
                        agree(&amount.text.render(), amount.form, curr),
                        rate_inline()
                    ),
                    amount.weight
                );
            });
        }
        if let Some(a) = unsigned.strip_prefix(&curr.written) {
            let a = a.strip_prefix(' ').unwrap_or(a);
            amounts(a, |amount| {
                add!(
                    format!(
                        "{field}currency: \"{}\" integer_part: \"{}{}\"{} preserve_order: true",
                        curr.written,
                        amount.text.render(),
                        amount.mark,
                        rate_field()
                    ),
                    amount.weight
                );
            });
        }
    }
    if rate.is_none() {
        for &curr in MINOR_WRITTEN_SUFFIXES.matching(unsigned) {
            if let Some(a) = unsigned.strip_suffix(&curr.written) {
                let a = space(a);
                if let Some(g) = number_graph(a, false) {
                    add!(format!("integer_part: \"{inline}{}\"", counted(g.value, curr)), weight(g));
                }
            }
            if let Some(a) = unsigned.strip_suffix(&curr.written) {
                let a = space(a);
                if let Some((major, minor)) = a.rsplit_once(' ') {
                    if let Some(n) = number_graph(minor, false) {
                        for &m in MAJOR_SUFFIXES.matching(major) {
                            if let Some(a) = major.strip_suffix(&m.written) {
                                let a = space(a);
                                if let Some(v) = number_graph(a, false) {
                                    add!(
                                        format!(
                                            "integer_part: \"{inline}{} {}\"",
                                            counted(v.value, m),
                                            counted(n.value, curr)
                                        ),
                                        weight(v) + weight(n)
                                    );
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    for curr in MINOR_EDGES.matching(unsigned) {
        let lead = unsigned.strip_prefix(&curr.written).map(|a| (true, a.strip_prefix(' ').unwrap_or(a)));
        let trail = unsigned.strip_suffix(&curr.written).map(|a| (false, a.strip_suffix(' ').unwrap_or(a)));
        for (leading, a) in lead.into_iter().chain(trail) {
            if let Some((i, f)) = a.split_once('.').or_else(|| a.split_once(',')) {
                if let Some(n) = number_graph(i, false) {
                    if f.len() == 2 && f.bytes().all(|c| c.is_ascii_digit()) && f != "00" {
                        if let Ok(cents) = f.parse::<u64>() {
                            add!(
                                {
                                    let parts = format!(
                                        "integer_part: \"{}{}\" fractional_part: \"{}{}\"",
                                        cardinal(n.value, Gender::Masc, Case::Nom),
                                        marker(n.value),
                                        cardinal(cents, Gender::Masc, Case::Nom),
                                        marker(cents)
                                    );
                                    if leading {
                                        format!(
                                            "{field}currency: \"{}\" {parts}{} preserve_order: true",
                                            curr.written,
                                            rate_field()
                                        )
                                    } else {
                                        format!(
                                            "{field}{parts} currency: \"{}\"{} preserve_order: true",
                                            curr.written,
                                            rate_field()
                                        )
                                    }
                                },
                                weight(n)
                            );
                        }
                    } else if f == "00" && !leading {
                        add!(
                            format!(
                                "{field}integer_part: \"{}{}\" currency: \"{}\"{} preserve_order: true",
                                cardinal(n.value, Gender::Masc, Case::Nom),
                                marker(n.value),
                                curr.written,
                                rate_field()
                            ),
                            weight(n) - 0.05
                        );
                    }
                }
            }
        }
    }
    let approx = if let Some(r) = core.strip_prefix('~') {
        Some(("около", r.strip_prefix(' ').unwrap_or(r)))
    } else if core.chars().next().is_some_and(super::whitelist::is_ru_alpha) {
        super::cardinal::GENITIVE_PREPOSITIONS
            .iter()
            .find_map(|p| core.strip_prefix(p).and_then(|r| r.strip_prefix(' ')).map(|r| (*p, r)))
    } else {
        None
    };
    if let Some((p, a)) = approx {
        for &curr in MAJOR_SUFFIXES.matching(a) {
            if let Some(a) = a.strip_suffix(&curr.written) {
                let a = space(a);
                if let Some(g) = number_graph(a, true) {
                    add!(
                        format!(
                            "integer_part: \"{p} {}{}\"",
                            agree(&g.render(Gender::Masc, Case::Gen), g.form, curr),
                            rate_inline()
                        ),
                        g.cost as f64 / 1_000_000.0 - 0.05
                    );
                }
            }
        }
    }
    if core.contains(['-', '–']) {
        for curr in MAJOR_EDGES.matching(core) {
            let trailing = core.strip_suffix(&curr.written).map(space);
            let leading = core.strip_prefix(&curr.written).map(|a| a.strip_prefix(' ').unwrap_or(a));
            for (lead, a) in [(false, trailing), (true, leading)] {
                if let Some(a) = a {
                    for sep in ['-', '–'] {
                        if let Some((left, right)) = a.split_once(sep) {
                            let left = left.strip_suffix(' ').unwrap_or(left);
                            let mut right = right.strip_prefix(' ').unwrap_or(right);
                            if lead {
                                if let Some(r) = right.strip_prefix(&curr.written) {
                                    right = r.strip_prefix(' ').unwrap_or(r);
                                }
                            }
                            if let (Some(x), Some(y)) = (range_number(left), range_number(right)) {
                                add!(
                                    format!(
                                        "integer_part: \"от {} до {}{}\"",
                                        cardinal(x, Gender::Masc, Case::Gen),
                                        gen_counted(y, curr),
                                        rate_inline()
                                    ),
                                    0.0
                                );
                            }
                        }
                    }
                }
            }
        }
    }
    best
}
fn has_currency(s: &str) -> bool {
    let Some((core, _)) = rate(s) else { return false };
    let unsigned = core
        .strip_prefix('-')
        .or_else(|| core.strip_prefix('~').map(|r| r.strip_prefix(' ').unwrap_or(r)))
        .or_else(|| core.strip_prefix('±').map(|r| r.strip_prefix(' ').unwrap_or(r)))
        .unwrap_or(core);
    MAJOR_EDGES
        .matching(unsigned)
        .chain(MINOR_EDGES.matching(unsigned))
        .any(|noun| unsigned.starts_with(&noun.written) || unsigned.ends_with(&noun.written))
        || MINOR_WRITTEN_SUFFIXES.matching(unsigned).iter().any(|noun| unsigned.ends_with(&noun.written))
}
pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !has_currency(s) {
        return;
    }
    if let Some(p) = parse_money(s, None) {
        out.push(Candidate { end, cost: p.cost, rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(p) = parse_money(s, Some(candidate.cost)) {
        candidate.tag = format!("money {{ {} }}", p.tag);
    }
}
