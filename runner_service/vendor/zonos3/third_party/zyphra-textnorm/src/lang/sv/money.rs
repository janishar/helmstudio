//! Swedish money classifier: leading/trailing currencies, major/minor units, ranges, and per-unit suffixes.

use super::number::cardinal;

crate::tsv!(CURRENCY_MAJOR, "sv/money/currency_major.tsv");
crate::tsv!(CURRENCY_MAJOR_NT, "sv/money/currency_major_nt.tsv");
crate::tsv!(CURRENCY_PLURALS, "sv/money/currency_plurals.tsv");
crate::tsv!(CURRENCY_MINOR_SG, "sv/money/currency_minor_singular.tsv");
crate::tsv!(CURRENCY_MINOR_PL, "sv/money/currency_minor_plural.tsv");
crate::tsv!(PER_UNIT, "sv/money/per_unit.tsv");

// The sentence-level range graph uses a deliberately restricted leading
// integer transducer, independently of the ordinary money classifier.
#[derive(Clone, Copy)]
struct RangeAmount<'a> {
    digits: &'a [char],
    currency: &'a str,
    common: bool,
    cost: crate::text::Cost,
}

fn range_amounts<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, RangeAmount<'a>)) {
    for (table, common) in [(&*CURRENCY_MAJOR, true), (&*CURRENCY_MAJOR_NT, false)] {
        for (row, symbol_end) in table.prefix_matches(text, start) {
            let mut number_start = symbol_end;
            while text.get(number_start).is_some_and(|c| crate::text::is_nemo_space(*c)) {
                number_start += 1;
            }
            let mut end = number_start;
            while end < text.len() && end - number_start < 9 && text[end].is_ascii_digit() {
                end += 1;
                let digits = &text[number_start..end];
                if digits.len() > 1 && digits[0] == '0' {
                    break;
                }
                let one = digits == ['1'];
                let mut magnitude_ones = 0;
                for group in digits.rchunks(3).skip(2) {
                    if group.last() == Some(&'1') && group[..group.len() - 1].iter().all(|c| *c == '0') {
                        magnitude_ones += 1;
                    }
                }
                let cost = crate::text::cost(-0.001) * magnitude_ones;
                if one {
                    emit(end, RangeAmount { digits, currency: &row.value, common, cost });
                } else {
                    for plural in CURRENCY_PLURALS.exact(&row.value) {
                        emit(end, RangeAmount { digits, currency: &plural.value, common, cost });
                    }
                }
            }
        }
    }
}

/// Emit every path through `graph_range_money`, with spelling deferred to the
/// selected synchronous sink closure.
pub(super) fn classify_range(text: &[char], start: usize, sink: &mut super::Sink<'_>) {
    range_amounts(text, start, |first_end, first| {
        for before in 0..=1 {
            let dash = first_end + before;
            if before == 1 && text.get(first_end) != Some(&' ') {
                continue;
            }
            if !matches!(text.get(dash), Some('-' | '–' | '—')) {
                continue;
            }
            for after in 0..=1 {
                let second_start = dash + 1 + after;
                if after == 1 && text.get(dash + 1) != Some(&' ') {
                    continue;
                }
                range_amounts(text, second_start, |end, second| {
                    sink.push(end, crate::text::cost(1.1) + first.cost + second.cost, 1000, || {
                        let render = |amount: RangeAmount<'_>| {
                            let digits: String = amount.digits.iter().collect();
                            let spoken = if digits == "1" {
                                if amount.common { "en".to_string() } else { "ett".to_string() }
                            } else {
                                cardinal(&digits, amount.common, true).expect("validated money-range digits")
                            };
                            let currency = amount.currency.replace(' ', "\u{a0}");
                            format!("money {{ currency_maj: \"{currency}\" integer_part: \"{spoken}\" }}")
                        };
                        format!("{} }} tokens {{ name: \"till\" }} tokens {{ {}", render(first), render(second))
                    });
                });
            }
        }
    });
}

#[derive(Clone, Copy)]
enum Amount<'a> {
    Integer(super::cardinal::CardinalRecipe<'a>),
    Decimal(super::decimal::DecimalRecipe<'a>),
    Kilo(super::cardinal::CardinalRecipe<'a>),
    Magnitude(super::cardinal::CardinalRecipe<'a>, &'static str, bool),
    DecimalMagnitude(super::decimal::DecimalRecipe<'a>, &'static str),
    Range(super::cardinal::CardinalRecipe<'a>, super::cardinal::CardinalRecipe<'a>),
}

impl Amount<'_> {
    fn append(self, out: &mut String) {
        match self {
            Self::Integer(i) => {
                out.push_str("integer_part: \"");
                i.append(out);
                out.push('"');
            }
            Self::Decimal(d) => out.push_str(&d.render()),
            Self::Kilo(i) => {
                let mut digits = [b'0'; 24];
                for (n, c) in i.raw.iter().enumerate() {
                    digits[n] = *c as u8;
                }
                let raw = std::str::from_utf8(&digits[..i.raw.len() + 3]).expect("ASCII digits");
                out.push_str("integer_part: \"");
                super::number::cardinal_into(out, raw, i.common, true).expect("validated shorthand");
                out.push('"');
            }
            Self::Magnitude(i, unit, singular) => {
                out.push_str("integer_part: \"");
                i.append(out);
                out.push_str("\" quantity: \"");
                out.push_str(unit);
                if !singular {
                    out.push_str("er");
                }
                out.push('"');
            }
            Self::DecimalMagnitude(d, unit) => {
                out.push_str(&d.render());
                out.push_str(" quantity: \"");
                out.push_str(unit);
                out.push('"');
            }
            Self::Range(a, b) => {
                out.push_str("integer_part: \"");
                a.append(out);
                out.push_str(" till ");
                b.append(out);
                out.push('"');
            }
        }
    }
}

#[derive(Clone, Copy)]
struct AmountPath<'a> {
    end: usize,
    cost: crate::text::Cost,
    amount: Amount<'a>,
    one: bool,
    decimal: bool,
}

fn space_end(text: &[char], mut at: usize) -> usize {
    while text.get(at).is_some_and(|c| crate::text::is_nemo_space(*c)) {
        at += 1;
    }
    at
}

fn amounts<'a>(text: &'a [char], start: usize, common: bool, emit: impl FnMut(AmountPath<'a>)) {
    let mut cardinals = [None; 24];
    let mut count = 0;
    super::cardinal::recipes(text, start, common, true, |integer| {
        cardinals[count] = Some(integer);
        count += 1;
    });
    amounts_with_cardinals(text, start, common, &cardinals[..count], emit);
}

fn amounts_with_cardinals<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'a>>],
    mut emit: impl FnMut(AmountPath<'a>),
) {
    for &integer in cardinals {
        let mut integer = integer.expect("populated cardinal recipe");
        integer.common = common;
        integer.counted = true;
        let one = integer.raw == ['1'];
        emit(AmountPath {
            end: integer.end,
            cost: integer.cost,
            amount: Amount::Integer(integer),
            one,
            decimal: false,
        });
        if integer.raw.iter().any(|c| !c.is_ascii_digit()) {
            continue;
        }
        let e = integer.end;
        if matches!(text.get(e), Some('k' | 'K')) && integer.raw != ['0'] && integer.raw.len() <= 21 {
            let mut groups = [0u8; 24];
            for (i, c) in integer.raw.iter().enumerate() {
                groups[i] = *c as u8 - b'0';
            }
            let n = integer.raw.len() + 3;
            let ones = groups[..n]
                .rchunks(3)
                .skip(2)
                .filter(|g| g.last() == Some(&1) && g[..g.len() - 1].iter().all(|d| *d == 0))
                .count();
            emit(AmountPath {
                end: e + 1,
                cost: crate::text::cost(-0.001) * ones as i64,
                amount: Amount::Kilo(integer),
                one: false,
                decimal: true,
            });
        }
        let suffix = if text.get(e) == Some(&' ') { e + 1 } else { e };
        if let Some((unit, _)) = match text.get(suffix) {
            Some('M') => Some(("miljon", true)),
            Some('B') => Some(("miljard", true)),
            _ => None,
        } {
            let mut i = integer;
            i.common = true;
            i.counted = false;
            emit(AmountPath {
                end: suffix + 1,
                cost: i.cost,
                amount: Amount::Magnitude(i, unit, one),
                one: false,
                decimal: true,
            });
        }
    }
    for separator in [',', '.'] {
        super::decimal::recipes_with_cardinals(text, start, common, separator, cardinals, |decimal| {
            let amount = Amount::Decimal(decimal);
            if decimal.quantity.is_some()
                && (separator != '.' || decimal.integer.is_some())
                && text.get(decimal.end - 1).is_some_and(|c| {
                    c.is_ascii_alphabetic() || matches!(*c, 'å' | 'ä' | 'ö' | 'ü' | 'é' | 'Å' | 'Ä' | 'Ö' | 'Ü' | 'É')
                })
            {
                emit(AmountPath { end: decimal.end, cost: decimal.cost, amount, one: false, decimal: true });
            }
            if decimal.quantity.is_none() && decimal.integer.is_some() && !text[start..decimal.end].contains(&' ') {
                let suffix = if text.get(decimal.end) == Some(&' ') { decimal.end + 1 } else { decimal.end };
                if let Some(unit) = match text.get(suffix) {
                    Some('M') => Some("miljoner"),
                    Some('B') => Some("miljarder"),
                    Some('k' | 'K') => Some("tusen"),
                    _ => None,
                } {
                    let mut d = decimal;
                    if let Some(i) = &mut d.integer {
                        i.common = unit != "tusen";
                    }
                    emit(AmountPath {
                        end: suffix + 1,
                        cost: d.cost,
                        amount: Amount::DecimalMagnitude(d, unit),
                        one: false,
                        decimal: true,
                    });
                }
            }
        });
    }
    trimmed_decimals(text, start, common, &mut emit);
}

fn trimmed_decimals<'a>(text: &'a [char], start: usize, common: bool, emit: &mut impl FnMut(AmountPath<'a>)) {
    let mut comma = start;
    let mut digits = [0u8; 24];
    let mut n = 0;
    while let Some(&c) = text.get(comma) {
        if c.is_ascii_digit() {
            if n == 24 || (n > 0 && digits[0] == 0) {
                return;
            }
            digits[n] = c as u8 - b'0';
            n += 1;
        } else if c != ' ' {
            break;
        }
        comma += 1;
    }
    if text.get(comma) != Some(&',') || !text.get(comma + 3).is_some_and(|c| c.is_ascii_digit() && *c != '0') {
        return;
    }
    let ones = digits[..n]
        .rchunks(3)
        .skip(2)
        .filter(|g| g.last() == Some(&1) && g[..g.len() - 1].iter().all(|d| *d == 0))
        .count();
    let cost = crate::text::cost(-0.001) * ones as i64;
    let integer = (n != 0).then_some(super::cardinal::CardinalRecipe {
        end: comma,
        cost,
        raw: &text[start..comma],
        common,
        counted: false,
    });
    super::decimal::fractional_recipes(text, comma + 1, |fractional| {
        if fractional.end != comma + 4 {
            return;
        }
        let decimal = super::decimal::DecimalRecipe {
            end: fractional.end,
            cost,
            integer,
            fractional: Some(fractional),
            quantity: None,
        };
        let mut end = fractional.end;
        emit(AmountPath { end, cost, amount: Amount::Decimal(decimal), one: false, decimal: true });
        while text.get(end) == Some(&'0') {
            end += 1;
            emit(AmountPath { end, cost, amount: Amount::Decimal(decimal), one: false, decimal: true });
        }
    });
}

#[derive(Clone, Copy)]
struct Currency<'a> {
    key: &'a str,
    major: &'a str,
    common: bool,
}

static SPELLED_COMMON: std::sync::LazyLock<crate::text::Table> =
    std::sync::LazyLock::new(|| spelled_table(&CURRENCY_MAJOR));
static SPELLED_NEUTER: std::sync::LazyLock<crate::text::Table> =
    std::sync::LazyLock::new(|| spelled_table(&CURRENCY_MAJOR_NT));
static PLURAL_COMMON: std::sync::LazyLock<crate::text::Table> =
    std::sync::LazyLock::new(|| plural_table(&CURRENCY_MAJOR));
static PLURAL_NEUTER: std::sync::LazyLock<crate::text::Table> =
    std::sync::LazyLock::new(|| plural_table(&CURRENCY_MAJOR_NT));

/// First characters of the source currency symbols, used before trie traversal.
static CURRENCY_INITIALS: std::sync::LazyLock<([bool; 128], Vec<char>)> = std::sync::LazyLock::new(|| {
    let mut ascii = [false; 128];
    let mut other = Vec::new();
    for row in CURRENCY_MAJOR.rows().iter().chain(CURRENCY_MAJOR_NT.rows()) {
        if let Some(c) = row.key.chars().next() {
            if c.is_ascii() {
                ascii[c as usize] = true;
            } else if !other.contains(&c) {
                other.push(c);
            }
        }
    }
    (ascii, other)
});

static SPELLED_INITIALS: std::sync::LazyLock<([bool; 128], Vec<char>)> = std::sync::LazyLock::new(|| {
    let mut ascii = [false; 128];
    let mut other = Vec::new();
    for row in SPELLED_COMMON.rows().iter().chain(SPELLED_NEUTER.rows()) {
        if let Some(c) = row.key.chars().next() {
            if c.is_ascii() {
                ascii[c as usize] = true;
            } else if !other.contains(&c) {
                other.push(c);
            }
        }
    }
    (ascii, other)
});

fn initial_matches(initials: &([bool; 128], Vec<char>), c: char) -> bool {
    if c.is_ascii() { initials.0[c as usize] } else { initials.1.contains(&c) }
}

fn currency_starts(text: &[char], start: usize) -> bool {
    let Some(&c) = text.get(start) else { return false };
    let possible = initial_matches(&CURRENCY_INITIALS, c);
    possible
        && (CURRENCY_MAJOR.prefix_matches(text, start).next().is_some()
            || CURRENCY_MAJOR_NT.prefix_matches(text, start).next().is_some())
}

// Index the source composition in major-row and plural-row order. Duplicate
// aliases remain separate paths, including their original equal-cost ties.
fn plural_table(major: &crate::text::Table) -> crate::text::Table {
    crate::text::Table::from_pairs(
        major
            .rows()
            .iter()
            .flat_map(|row| CURRENCY_PLURALS.exact(&row.value).map(|plural| (row.key.clone(), plural.value.clone()))),
    )
}

fn spelled_table(major: &crate::text::Table) -> crate::text::Table {
    crate::text::Table::from_pairs(major.rows().iter().flat_map(|row| {
        CURRENCY_PLURALS.exact(&row.value).map(|plural| (plural.value.replace(' ', "\u{a0}"), plural.value.clone()))
    }))
}

fn currencies<'a>(
    text: &'a [char],
    at: usize,
    one: bool,
    spelled: bool,
    gender: Option<bool>,
    mut emit: impl FnMut(usize, Currency<'a>),
) {
    let Some(&initial) = text.get(at) else { return };
    if !initial_matches(&CURRENCY_INITIALS, initial)
        && !(spelled && !one && initial_matches(&SPELLED_INITIALS, initial))
    {
        return;
    }
    for common in [true, false] {
        if gender.is_some_and(|wanted| wanted != common) {
            continue;
        }
        let table = match (common, one) {
            (true, true) => &*CURRENCY_MAJOR,
            (false, true) => &*CURRENCY_MAJOR_NT,
            (true, false) => &*PLURAL_COMMON,
            (false, false) => &*PLURAL_NEUTER,
        };
        for (row, end) in table.prefix_matches(text, at) {
            emit(end, Currency { key: &row.key, major: &row.value, common });
        }
        if spelled && !one {
            let table = if common { &*SPELLED_COMMON } else { &*SPELLED_NEUTER };
            for (row, end) in table.prefix_matches(text, at) {
                emit(end, Currency { key: &row.key, major: &row.value, common });
            }
        }
    }
}

fn minor_currencies<'a>(text: &'a [char], at: usize, mut emit: impl FnMut(usize, Currency<'a>)) {
    let Some(&initial) = text.get(at) else { return };
    if !initial_matches(&CURRENCY_INITIALS, initial) {
        return;
    }
    for (table, common) in [(&*CURRENCY_MAJOR, true), (&*CURRENCY_MAJOR_NT, false)] {
        for (row, end) in table.prefix_matches(text, at) {
            if !common && CURRENCY_MAJOR.contains_key(&row.key) {
                continue;
            }
            emit(end, Currency { key: &row.key, major: &row.value, common });
        }
    }
}

#[derive(Clone, Copy)]
struct MoneyPath<'a> {
    amount: Option<Amount<'a>>,
    major: Option<&'a str>,
    minor: Option<(u8, &'a str)>,
    leading: bool,
    preserve: bool,
    negative: bool,
}

impl MoneyPath<'_> {
    fn render(self, per: Option<&str>) -> String {
        let mut tag = String::with_capacity(96);
        tag.push_str("money { ");
        if self.negative {
            tag.push_str("negative: \"true\" ");
        }
        let order = if self.leading { [0, 1, 2] } else { [1, 2, 0] };
        let mut first = true;
        for field in order {
            let present = match field {
                0 => self.major.is_some(),
                1 => self.amount.is_some(),
                _ => self.minor.is_some(),
            };
            if !present {
                continue;
            }
            if !first {
                tag.push(' ');
            }
            first = false;
            match field {
                0 => {
                    tag.push_str("currency_maj: \"");
                    tag.extend(
                        self.major.expect("present currency").chars().map(|c| if c == ' ' { '\u{a0}' } else { c }),
                    );
                    tag.push('"');
                }
                1 => self.amount.expect("present amount").append(&mut tag),
                _ => {
                    let (number, unit) = self.minor.expect("present minor currency");
                    tag.push_str("fractional_part: \"");
                    if number == 1 {
                        tag.push_str(if unit == "öre" { "ett" } else { "en" });
                    } else {
                        super::number::under_hundred(&mut tag, number, true);
                    }
                    tag.push_str("\" currency_min: \"");
                    tag.push_str(unit);
                    tag.push('"');
                }
            }
        }
        if self.preserve {
            tag.push_str(" preserve_order: true");
        }
        if let Some(per) = per {
            tag.push_str(" morphosyntactic_features: \"");
            tag.extend(per.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
            tag.push('"');
        }
        tag.push_str(" }");
        tag
    }
}

fn emit_money<'a>(
    text: &'a [char],
    end: usize,
    cost: crate::text::Cost,
    path: MoneyPath<'a>,
    sink: &mut super::Sink<'_>,
) {
    sink.push(end, cost, 900, || path.render(None));
    for at in [end, end + usize::from(text.get(end) == Some(&' '))] {
        if at != end && text.get(end) != Some(&' ') {
            continue;
        }
        for (row, per_end) in PER_UNIT.prefix_matches(text, at) {
            sink.push(per_end, cost, 900, || path.render(Some(&row.value)));
        }
        if at == end && text.get(end) != Some(&' ') {
            break;
        }
    }
}

fn zero_ends(text: &[char], at: usize, mut emit: impl FnMut(usize)) {
    emit(at);
    if text.get(at) == Some(&',') {
        let mut end = at + 1;
        if matches!(text.get(end), Some('-' | '–')) {
            emit(end + 1);
        }
        while text.get(end) == Some(&'0') {
            end += 1;
            emit(end);
        }
    }
    if text.get(at..at + 3) == Some(&['.', '0', '0']) {
        emit(at + 3);
    }
}

fn fractional_minor(text: &[char], at: usize, mut emit: impl FnMut(usize, u8)) {
    let Some(&separator @ (',' | '.')) = text.get(at) else { return };
    let mut end = at + 1;
    let mut value = 0u8;
    while let Some(&c) = text.get(end) {
        if !c.is_ascii_digit() || (separator == '.' && end - at > 2) {
            break;
        }
        let digit = c as u8 - b'0';
        let len = end - at;
        if len == 1 {
            value = digit * 10;
        } else if len == 2 {
            value += digit;
        } else if digit != 0 {
            break;
        }
        end += 1;
        if value > 0 {
            emit(end, value);
        }
    }
}

fn minor_unit<'a>(currency: Currency<'a>, number: u8) -> Option<&'a str> {
    if !currency.common && CURRENCY_MAJOR.contains_key(currency.key) {
        return None;
    }
    let sg = CURRENCY_MINOR_SG.exact(currency.key).next()?;
    let pl = CURRENCY_MINOR_PL.exact(currency.key).next()?;
    Some(if number == 1 { &sg.value } else { &pl.value })
}

fn add_minor<'a>(
    text: &'a [char],
    integer: Option<super::cardinal::CardinalRecipe<'a>>,
    at: usize,
    currency: Currency<'a>,
    leading: bool,
    negative: bool,
    sink: &mut super::Sink<'_>,
) {
    fractional_minor(text, at, |fraction_end, number| {
        let Some(unit) = minor_unit(currency, number) else { return };
        let path = MoneyPath {
            amount: integer.map(Amount::Integer),
            major: integer.map(|_| currency.major),
            minor: Some((number, unit)),
            leading,
            preserve: integer.is_none(),
            negative,
        };
        let cost = crate::text::cost(1.1) + crate::text::cost(-0.0001) + integer.map_or(0, |i| i.cost);
        if leading {
            emit_money(text, fraction_end, cost, path, sink);
        } else {
            for skip in 0..=1 {
                if skip == 1 && text.get(fraction_end) != Some(&' ') {
                    continue;
                }
                currencies(
                    text,
                    fraction_end + skip,
                    integer.is_some_and(|i| i.raw == ['1']),
                    false,
                    Some(currency.common),
                    |end, matched| {
                        if matched.key == currency.key && matched.common == currency.common {
                            emit_money(text, end, cost, path, sink);
                        }
                    },
                );
            }
        }
    });
}

fn classify_trailing_range(
    text: &[char],
    cardinals: &[Option<super::cardinal::CardinalRecipe<'_>>],
    negative: bool,
    sink: &mut super::Sink<'_>,
) {
    for common in [true, false] {
        for &first in cardinals {
            let mut first = first.expect("populated cardinal recipe");
            first.common = common;
            zero_ends(text, first.end, |first_end| {
                for spaced in [false, true] {
                    let dash = first_end + usize::from(spaced);
                    if spaced && text.get(first_end) != Some(&' ') {
                        continue;
                    }
                    if !matches!(text.get(dash), Some('-' | '–' | '—')) {
                        continue;
                    }
                    let second_start = dash + 1 + usize::from(spaced);
                    if spaced && text.get(dash + 1) != Some(&' ') {
                        continue;
                    }
                    super::cardinal::recipes(text, second_start, common, true, |second| {
                        zero_ends(text, second.end, |second_end| {
                            let currency_at = space_end(text, second_end);
                            currencies(text, currency_at, false, true, Some(common), |end, currency| {
                                let path = MoneyPath {
                                    amount: Some(Amount::Range(first, second)),
                                    major: Some(currency.major),
                                    minor: None,
                                    leading: false,
                                    preserve: false,
                                    negative,
                                };
                                emit_money(text, end, crate::text::cost(1.1) + first.cost + second.cost, path, sink);
                            });
                        });
                    });
                }
            });
        }
    }
}

fn emit_cent(
    text: &[char],
    end: usize,
    integer: super::cardinal::CardinalRecipe<'_>,
    negative: bool,
    sink: &mut super::Sink<'_>,
) {
    let render = |per: Option<&str>| {
        let sign = if negative { "negative: \"true\" " } else { "" };
        let spoken = integer.render();
        let suffix =
            per.map_or_else(String::new, |p| format!(" morphosyntactic_features: \"{}\"", p.replace(' ', "\u{a0}")));
        format!("money {{ {sign}fractional_part: \"{spoken}\" currency_min: \"cent\" preserve_order: true{suffix} }}")
    };
    let cost = crate::text::cost(1.1) + integer.cost;
    sink.push(end, cost, 900, || render(None));
    for at in [end, end + usize::from(text.get(end) == Some(&' '))] {
        for (row, per_end) in PER_UNIT.prefix_matches(text, at) {
            sink.push(per_end, cost, 900, || render(Some(&row.value)));
        }
        if at == end && text.get(end) != Some(&' ') {
            break;
        }
    }
}

fn classify_cent(
    text: &[char],
    start: usize,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'_>>],
    negative: bool,
    sink: &mut super::Sink<'_>,
) {
    if text.get(start) == Some(&'¢') {
        for skip in 0..=1 {
            let at = start + 1 + skip;
            if skip == 1 && text.get(start + 1) != Some(&' ') {
                continue;
            }
            super::cardinal::recipes(text, at, true, true, |integer| {
                emit_cent(text, integer.end, integer, negative, sink);
            });
        }
    }
    for &integer in cardinals {
        let mut integer = integer.expect("populated cardinal recipe");
        integer.common = true;
        for skip in 0..=1 {
            if skip == 1 && text.get(integer.end) != Some(&' ') {
                continue;
            }
            let at = integer.end + skip;
            if text.get(at) == Some(&'¢') {
                emit_cent(text, at + 1, integer, negative, sink);
            }
        }
    }
}

/// Full money classifier entry point; token rendering is deferred to `Sink`.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink<'_>) {
    // All other branches begin with an amount, sign, cent symbol, or a
    // source currency key. The tables already provide trie prefix lookup.
    let Some(&initial) = text.get(start) else { return };
    let starts_currency = currency_starts(text, start);
    if !initial.is_ascii_digit() && !matches!(initial, '-' | '−' | '¢' | ',' | '.' | ' ') && !starts_currency {
        return;
    }
    if starts_currency && sink.wants(1000) {
        classify_range(text, start, sink);
    }
    if !sink.wants(900) {
        return;
    }
    for negative in [false, true] {
        let mut begin = start;
        if negative {
            if !matches!(text.get(begin), Some('-' | '−')) {
                continue;
            }
            begin += 1;
            if text.get(begin) == Some(&' ') {
                begin += 1;
            }
        }
        let mut cardinals = [None; 24];
        let mut count = 0;
        super::cardinal::recipes(text, begin, false, true, |integer| {
            cardinals[count] = Some(integer);
            count += 1;
        });
        classify_cent(text, begin, &cardinals[..count], negative, sink);
        if super::numeric_range_marker(text, begin) {
            classify_trailing_range(text, &cardinals[..count], negative, sink);
        }
        let leading_currency = if begin == start { starts_currency } else { currency_starts(text, begin) };
        if leading_currency {
            for one in [true, false] {
                currencies(text, begin, one, false, None, |currency_end, currency| {
                    let number_start = space_end(text, currency_end);
                    amounts(text, number_start, currency.common, |amount| {
                        if amount.one != one {
                            return;
                        }
                        let path = MoneyPath {
                            amount: Some(amount.amount),
                            major: Some(currency.major),
                            minor: None,
                            leading: true,
                            preserve: false,
                            negative,
                        };
                        let cost = crate::text::cost(1.1) + amount.cost;
                        if amount.decimal {
                            emit_money(text, amount.end, cost, path, sink);
                        } else {
                            zero_ends(text, amount.end, |end| emit_money(text, end, cost, path, sink));
                            if let Amount::Integer(integer) = amount.amount {
                                if integer.raw != ['0']
                                    && (number_start == currency_end
                                        || (number_start == currency_end + 1 && text[currency_end] == ' '))
                                    && (integer.raw.len() <= 12 || (integer.raw.len() <= 15 && integer.raw[0] != '0'))
                                {
                                    add_minor(text, Some(integer), amount.end, currency, true, negative, sink);
                                }
                            }
                        }
                    });
                });
            }
            minor_currencies(text, begin, |currency_end, currency| {
                for skip in 0..=1 {
                    if skip == 1 && text.get(currency_end) != Some(&' ') {
                        continue;
                    }
                    let at = currency_end + skip;
                    for zero in 0..=1 {
                        if zero == 1 && text.get(at) != Some(&'0') {
                            continue;
                        }
                        add_minor(text, None, at + zero, currency, true, negative, sink);
                    }
                }
            });
        }
        for common in [true, false] {
            amounts_with_cardinals(text, begin, common, &cardinals[..count], |amount| {
                let after_amount = |at: usize, sink: &mut super::Sink<'_>| {
                    let currency_at = space_end(text, at);
                    currencies(text, currency_at, amount.one, true, Some(common), |end, currency| {
                        let path = MoneyPath {
                            amount: Some(amount.amount),
                            major: Some(currency.major),
                            minor: None,
                            leading: false,
                            preserve: false,
                            negative,
                        };
                        emit_money(text, end, crate::text::cost(1.1) + amount.cost, path, sink);
                    });
                };
                if amount.decimal {
                    after_amount(amount.end, sink);
                } else {
                    zero_ends(text, amount.end, |end| after_amount(end, sink));
                    if let Amount::Integer(integer) = amount.amount {
                        if integer.raw != ['0'] {
                            fractional_minor(text, amount.end, |fraction_end, number| {
                                for skip in 0..=1 {
                                    if skip == 1 && text.get(fraction_end) != Some(&' ') {
                                        continue;
                                    }
                                    currencies(
                                        text,
                                        fraction_end + skip,
                                        amount.one,
                                        false,
                                        Some(common),
                                        |end, currency| {
                                            let Some(unit) = minor_unit(currency, number) else { return };
                                            let path = MoneyPath {
                                                amount: Some(Amount::Integer(integer)),
                                                major: Some(currency.major),
                                                minor: Some((number, unit)),
                                                leading: false,
                                                preserve: false,
                                                negative,
                                            };
                                            emit_money(
                                                text,
                                                end,
                                                crate::text::cost(1.1) + crate::text::cost(-0.0001) + amount.cost,
                                                path,
                                                sink,
                                            );
                                        },
                                    );
                                }
                            });
                        }
                    }
                }
            });
        }
        for zero in 0..=1 {
            if zero == 1 && text.get(begin) != Some(&'0') {
                continue;
            }
            fractional_minor(text, begin + zero, |fraction_end, number| {
                for skip in 0..=1 {
                    if skip == 1 && text.get(fraction_end) != Some(&' ') {
                        continue;
                    }
                    minor_currencies(text, fraction_end + skip, |end, currency| {
                        let Some(unit) = minor_unit(currency, number) else { return };
                        let path = MoneyPath {
                            amount: None,
                            major: None,
                            minor: Some((number, unit)),
                            leading: false,
                            preserve: true,
                            negative,
                        };
                        emit_money(text, end, crate::text::cost(1.1) + crate::text::cost(-0.0001), path, sink);
                    });
                }
            });
        }
    }
}
