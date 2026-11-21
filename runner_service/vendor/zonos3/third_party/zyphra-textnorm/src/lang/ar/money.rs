//! Arabic money classifier and verbalizer.
//! Matching `ar/taggers/money.py` and `ar/verbalizers/money.py`.

use super::cardinal::{parse_cardinal_span, spell_cardinal, zero_suffix_cost};
use super::char_util::{token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

crate::tsv!(CURRENCY_MAJOR, "ar/money/currency_major.tsv");
crate::tsv!(LOCAL_CURRENCY, "ar/money/local_currency.tsv");
crate::tsv!(CURRENCY_MIN_SINGULAR, "ar/money/currency_minor_singular.tsv");
crate::tsv!(CURRENCY_MIN_PLURAL, "ar/money/currency_minor_plural.tsv");

// Preserve row order while avoiding a full currency-table scan at every token.
static CURRENCY_INITIALS: LazyLock<[HashMap<char, Vec<usize>>; 2]> = LazyLock::new(|| {
    let mut indexes = [HashMap::<char, Vec<usize>>::new(), HashMap::<char, Vec<usize>>::new()];
    for (index, rows) in [CURRENCY_MAJOR.rows(), LOCAL_CURRENCY.rows()].into_iter().enumerate() {
        for (i, row) in rows.iter().enumerate() {
            if let Some(ch) = row.key.chars().next() {
                indexes[index].entry(ch).or_default().push(i);
            }
        }
    }
    indexes
});

const PER_UNIT_MAP: &[(&str, &str)] = &[
    ("h", "في الساعة"),
    ("hr", "في الساعة"),
    ("hrs", "في الساعة"),
    ("hour", "في الساعة"),
    ("ساعة", "في الساعة"),
    ("min", "في الدقيقة"),
    ("دقيقة", "في الدقيقة"),
    ("s", "في الثانية"),
    ("sec", "في الثانية"),
    ("ثانية", "في الثانية"),
    ("d", "في اليوم"),
    ("day", "في اليوم"),
    ("يوم", "في اليوم"),
    ("wk", "في الأسبوع"),
    ("week", "في الأسبوع"),
    ("أسبوع", "في الأسبوع"),
    ("mo", "في الشهر"),
    ("month", "في الشهر"),
    ("شهر", "في الشهر"),
    ("yr", "في السنة"),
    ("y", "في السنة"),
    ("year", "في السنة"),
    ("سنة", "في السنة"),
    ("عام", "في السنة"),
    ("kg", "لكل كيلوغرام"),
    ("كغ", "لكل كيلوغرام"),
    ("كجم", "لكل كيلوغرام"),
    ("g", "لكل غرام"),
    ("غ", "لكل غرام"),
    ("جم", "لكل غرام"),
    ("mg", "لكل مليغرام"),
    ("t", "لكل طن"),
    ("طن", "لكل طن"),
    ("lb", "لكل رطل"),
    ("oz", "لكل أونصة"),
    ("L", "لكل لتر"),
    ("l", "لكل لتر"),
    ("لتر", "لكل لتر"),
    ("mL", "لكل مليلتر"),
    ("ml", "لكل مليلتر"),
    ("m", "لكل متر"),
    ("م", "لكل متر"),
    ("km", "لكل كيلومتر"),
    ("كم", "لكل كيلومتر"),
    ("m²", "لكل متر مربع"),
    ("m2", "لكل متر مربع"),
    ("م²", "لكل متر مربع"),
    ("m³", "لكل متر مكعب"),
    ("m3", "لكل متر مكعب"),
    ("م³", "لكل متر مكعب"),
    ("kWh", "لكل كيلوواط ساعة"),
    ("MWh", "لكل ميغاواط ساعة"),
    ("unit", "لكل وحدة"),
    ("وحدة", "لكل وحدة"),
    ("قطعة", "لكل قطعة"),
    ("person", "لكل شخص"),
    ("شخص", "لكل شخص"),
    ("فرد", "لكل فرد"),
];

pub static CURRENCY_SYMBOLS: LazyLock<Vec<String>> = LazyLock::new(|| {
    let mut syms = Vec::new();
    for r in CURRENCY_MAJOR.rows() {
        syms.push(r.key.clone());
    }
    for r in LOCAL_CURRENCY.rows() {
        syms.push(r.key.clone());
    }
    syms.sort_unstable_by(|a, b| b.len().cmp(&a.len()).then_with(|| a.cmp(b)));
    syms.dedup();
    syms
});

pub fn get_range_currency_name(sym: &str) -> Option<&'static str> {
    CURRENCY_MAJOR
        .rows()
        .iter()
        .find(|r| r.key == sym)
        .or_else(|| LOCAL_CURRENCY.rows().iter().find(|r| r.key == sym))
        .map(|r| r.value.as_str())
}

pub struct MoneyClassifier<'a> {
    pub punct: &'a Punctuation,
}

#[derive(Clone, Copy)]
enum Form {
    Integer,
    CurrencyFirst,
    Minor,
    MinorOnly,
    MinorWithoutUnit,
    MinorFallback,
    Shorthand(char, Option<(u64, usize)>),
    Cents,
}

#[derive(Clone, Copy)]
struct Path {
    end: usize,
    currency: &'static str,
    minor: Option<&'static str>,
    quantity: Option<&'static str>,
    number: u64,
    fraction: u64,
    form: Form,
    prefix: bool,
    negative: bool,
    weight: f64,
    grouped_bonus: bool,
}

fn matches_at(text: &[char], start: usize, value: &str) -> Option<usize> {
    let mut end = start;
    for c in value.chars() {
        if text.get(end) != Some(&c) {
            return None;
        }
        end += 1;
    }
    Some(end)
}

fn skip_space(text: &[char], pos: usize) -> usize {
    pos + usize::from(text.get(pos) == Some(&' '))
}

// The money graph additionally accepts every correctly grouped cardinal, without the
// cardinal tagger's -0.01 grouped-number bonus.
fn integer(text: &[char], start: usize, end: usize) -> Option<(u64, f64)> {
    if !text[start..end].contains(&',') {
        return parse_cardinal_span(text, start, end, true);
    }
    let mut value = 0u64;
    let mut group = 0;
    let mut groups = 0;
    for (i, &c) in text[start..end].iter().enumerate() {
        if c == ',' {
            if (groups == 0 && (!(1..=3).contains(&group) || text[start] == '0')) || (groups != 0 && group != 3) {
                return None;
            }
            groups += 1;
            group = 0;
        } else if c.is_ascii_digit() {
            if i == 0 && c == '0' {
                return None;
            }
            value = value.checked_mul(10)?.checked_add(c.to_digit(10)? as u64)?;
            group += 1;
        } else {
            return None;
        }
    }
    if group != 3 || groups == 0 || groups > 3 || value >= 1_000_000_000_000 {
        return None;
    }
    Some((value, zero_suffix_cost(value)))
}

fn minor_integer_domain(text: &[char], start: usize, end: usize) -> bool {
    let s = &text[start..end];
    if s.contains(&',') {
        return s.iter().filter(|&&c| c == ',').count() <= 2;
    }
    if s.iter().all(|&c| c == '0') {
        return true;
    }
    if s[0] == '0' {
        return s.iter().take_while(|&&c| c == '0').count() + 1 == s.len();
    }
    s.len() <= 7
}

fn fraction(text: &[char], start: usize, end: usize) -> Option<u64> {
    if start == end || !text[start..end].iter().all(char::is_ascii_digit) {
        return None;
    }
    let mut last = end;
    while last > start && text[last - 1] == '0' {
        last -= 1;
    }
    let len = last - start;
    if len == 0 || len > 2 {
        return None;
    }
    let a = text[start].to_digit(10)? as u64;
    if len == 1 {
        if a == 0 { None } else { Some(a * 10) }
    } else {
        let b = text[start + 1].to_digit(10)? as u64;
        if a == 0 && b == 0 { None } else { Some(a * 10 + b) }
    }
}

fn emit(text: &[char], punct: &Punctuation, path: Path, f: &mut impl FnMut(Path)) {
    if token_end(text, path.end, punct) {
        f(path);
        if path.grouped_bonus {
            f(Path { weight: path.weight - 0.01, ..path });
        }
    }
    let mut p = skip_space(text, path.end);
    if text.get(p) != Some(&'/') {
        return;
    }
    p = skip_space(text, p + 1);
    for &(key, value) in PER_UNIT_MAP {
        if let Some(end) = matches_at(text, p, key) {
            if token_end(text, end, punct) {
                let unit_path = Path { end, quantity: Some(value), ..path };
                f(unit_path);
                if path.grouped_bonus {
                    f(Path { weight: path.weight - 0.01, ..unit_path });
                }
            }
        }
    }
}

fn paths(text: &[char], start: usize, punct: &Punctuation, mut f: impl FnMut(Path)) {
    if !token_start(text, start, punct) {
        return;
    }
    let mut pos = start;
    let negative = matches!(text.get(pos), Some('-' | '−'));
    if negative {
        pos = skip_space(text, pos + 1);
    }
    if pos >= text.len() {
        return;
    }
    if !text[pos].is_ascii_digit()
        && text[pos] != '¢'
        && !CURRENCY_INITIALS.iter().any(|index| index.contains_key(&text[pos]))
    {
        return;
    }

    // Cents are a separate currency graph, with no leading zero and at most six digits.
    for prefix in [true, false] {
        let nstart = if prefix {
            if text[pos] != '¢' {
                continue;
            }
            skip_space(text, pos + 1)
        } else {
            pos
        };
        for nend in nstart + 1..=text.len().min(nstart + 6) {
            if !text[nend - 1].is_ascii_digit() || text[nstart] == '0' {
                break;
            }
            let end = if prefix {
                nend
            } else {
                let p = skip_space(text, nend);
                if text.get(p) != Some(&'¢') {
                    continue;
                }
                p + 1
            };
            if let Some((number, weight)) = parse_cardinal_span(text, nstart, nend, false) {
                emit(
                    text,
                    punct,
                    Path {
                        end,
                        currency: "سنت",
                        minor: None,
                        quantity: None,
                        number,
                        fraction: 0,
                        form: Form::Cents,
                        prefix,
                        negative,
                        weight,
                        grouped_bonus: false,
                    },
                    &mut f,
                );
            }
        }
    }

    for prefix in [true, false] {
        for (table_index, (local, rows)) in
            [(false, CURRENCY_MAJOR.rows()), (true, LOCAL_CURRENCY.rows())].into_iter().enumerate()
        {
            let index = &CURRENCY_INITIALS[table_index];
            let mut matches = Vec::new();
            if prefix {
                if let Some(indices) = text.get(pos).and_then(|ch| index.get(ch)) {
                    matches.extend(indices.iter().copied().filter(|&i| matches_at(text, pos, &rows[i].key).is_some()));
                }
            } else if text[pos].is_ascii_digit() {
                for nend in pos + 1..=text.len() {
                    let c = text[nend - 1];
                    if !(c.is_ascii_digit()
                        || c == '.'
                        || c == ','
                        || matches!(c, 'k' | 'K' | 'm' | 'M' | 'b' | 'B' | 't' | 'T'))
                    {
                        break;
                    }
                    let s = skip_space(text, nend);
                    if let Some(indices) = text.get(s).and_then(|ch| index.get(ch)) {
                        matches
                            .extend(indices.iter().copied().filter(|&i| matches_at(text, s, &rows[i].key).is_some()));
                    }
                }
                matches.sort_unstable();
                matches.dedup();
            }
            for i in matches {
                let row = &rows[i];
                let sym = row.key.as_str();
                let alpha = sym.is_ascii() && sym.chars().any(|c| c.is_ascii_alphabetic());
                let (nstart, currency_end) = if prefix {
                    let Some(end) = matches_at(text, pos, sym) else { continue };
                    (skip_space(text, end), end)
                } else {
                    (pos, 0)
                };
                if nstart >= text.len() || !text[nstart].is_ascii_digit() {
                    continue;
                }
                // Every numeric endpoint is considered: a shorter path can be followed
                // by punctuation or by a currency symbol that shares a prefix.
                for nend in nstart + 1..=text.len() {
                    let c = text[nend - 1];
                    if !(c.is_ascii_digit()
                        || c == '.'
                        || c == ','
                        || matches!(c, 'k' | 'K' | 'm' | 'M' | 'b' | 'B' | 't' | 'T'))
                    {
                        break;
                    }
                    let end = if prefix {
                        nend
                    } else {
                        let s = skip_space(text, nend);
                        if alpha && !local && s == nend {
                            continue;
                        }
                        let Some(end) = matches_at(text, s, sym) else { continue };
                        end
                    };
                    let _ = currency_end;
                    let numeric_end = text[nstart..nend].iter().position(|&c| c == '.').map_or(nend, |i| nstart + i);
                    let grouped_bonus = text[nstart..numeric_end].contains(&',')
                        && parse_cardinal_span(text, nstart, numeric_end, true).is_some();
                    let base = Path {
                        end,
                        currency: row.value.as_str(),
                        minor: None,
                        quantity: None,
                        number: 0,
                        fraction: 0,
                        form: Form::Integer,
                        prefix,
                        negative,
                        weight: if prefix { 0.0 } else { 0.01 },
                        grouped_bonus,
                    };
                    let last = text[nend - 1];
                    if matches!(last, 'k' | 'K' | 'm' | 'M' | 'b' | 'B' | 't' | 'T') {
                        let body = &text[nstart..nend - 1];
                        let sep = body.iter().position(|&x| x == '.' || x == ',');
                        let int_end = sep.map_or(nend - 1, |i| nstart + i);
                        let len = int_end - nstart;
                        if len == 0
                            || len > 3
                            || (len > 1 && text[nstart] == '0')
                            || !text[nstart..int_end].iter().all(char::is_ascii_digit)
                        {
                            continue;
                        }
                        let Some((number, number_cost)) = parse_cardinal_span(text, nstart, int_end, false) else {
                            continue;
                        };
                        if let Some(i) = sep {
                            let fs = nstart + i + 1;
                            let flen = nend - 1 - fs;
                            if !(1..=2).contains(&flen) || !text[fs..nend - 1].iter().all(char::is_ascii_digit) {
                                continue;
                            }
                            let Some((frac, fc)) = parse_cardinal_span(text, fs, nend - 1, true) else { continue };
                            emit(
                                text,
                                punct,
                                Path {
                                    number,
                                    form: Form::Shorthand(last, Some((frac, flen))),
                                    weight: base.weight + number_cost + fc,
                                    ..base
                                },
                                &mut f,
                            );
                        } else if number != 0 {
                            let (value, extra) = match last {
                                'k' | 'K' => (number * 1_000, 0.0),
                                'm' | 'M' => (number * 1_000_000, 0.0),
                                'b' | 'B' => (number * 1_000_000_000, 0.0),
                                _ => (number, number_cost),
                            };
                            emit(
                                text,
                                punct,
                                Path {
                                    number,
                                    form: Form::Shorthand(last, None),
                                    weight: base.weight
                                        + if matches!(last, 't' | 'T') { extra } else { zero_suffix_cost(value) },
                                    ..base
                                },
                                &mut f,
                            );
                        }
                        continue;
                    }
                    // A plain amount may end in a deleted all-zero decimal.
                    let dot = text[nstart..nend].iter().position(|&x| x == '.').map(|i| nstart + i);
                    let int_end = dot.unwrap_or(nend);
                    if let Some((number, number_cost)) = integer(text, nstart, int_end) {
                        let raw_one = int_end == nstart + 1 && text[nstart] == '1';
                        let nonzero = text[nstart..int_end].iter().any(|&c| c.is_ascii_digit() && c != '0');
                        let zero_suffix =
                            dot.map_or(true, |d| d + 1 < nend && text[d + 1..nend].iter().all(|&c| c == '0'));
                        if zero_suffix && (nonzero || raw_one) {
                            if !prefix || local || !raw_one {
                                emit(text, punct, Path { number, weight: base.weight + number_cost, ..base }, &mut f);
                            }
                            if prefix && !local && raw_one {
                                emit(
                                    text,
                                    punct,
                                    Path { number, form: Form::CurrencyFirst, weight: number_cost, ..base },
                                    &mut f,
                                );
                            }
                        }
                        if let Some(d) = dot {
                            if let Some(frac) = fraction(text, d + 1, nend) {
                                if minor_integer_domain(text, nstart, int_end) && (nonzero || raw_one) {
                                    let ms = CURRENCY_MIN_SINGULAR.exact(sym).next().map(|r| r.value.as_str());
                                    let mp = CURRENCY_MIN_PLURAL.exact(sym).next().map(|r| r.value.as_str());
                                    let fc = zero_suffix_cost(frac);
                                    if let (Some(s), Some(p)) = (ms, mp) {
                                        emit(
                                            text,
                                            punct,
                                            Path {
                                                number,
                                                fraction: frac,
                                                minor: Some(if (2..=10).contains(&frac) { p } else { s }),
                                                form: Form::Minor,
                                                weight: base.weight + number_cost + fc,
                                                grouped_bonus: false,
                                                ..base
                                            },
                                            &mut f,
                                        );
                                        emit(
                                            text,
                                            punct,
                                            Path {
                                                number,
                                                fraction: frac,
                                                form: Form::MinorFallback,
                                                weight: base.weight + number_cost + fc + 0.0001,
                                                grouped_bonus: false,
                                                ..base
                                            },
                                            &mut f,
                                        );
                                    } else if prefix {
                                        emit(
                                            text,
                                            punct,
                                            Path {
                                                number,
                                                fraction: frac,
                                                form: Form::MinorWithoutUnit,
                                                weight: number_cost + fc,
                                                grouped_bonus: false,
                                                ..base
                                            },
                                            &mut f,
                                        );
                                    }
                                }
                                if int_end == nstart + 1 && text[nstart] == '0' {
                                    let ms = CURRENCY_MIN_SINGULAR.exact(sym).next().map(|r| r.value.as_str());
                                    let mp = CURRENCY_MIN_PLURAL.exact(sym).next().map(|r| r.value.as_str());
                                    if let (Some(s), Some(p)) = (ms, mp) {
                                        emit(
                                            text,
                                            punct,
                                            Path {
                                                fraction: frac,
                                                minor: Some(if (2..=10).contains(&frac) { p } else { s }),
                                                form: Form::MinorOnly,
                                                weight: base.weight + zero_suffix_cost(frac),
                                                ..base
                                            },
                                            &mut f,
                                        );
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

impl Classifier for MoneyClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut rank = 0;
        paths(text, start, self.punct, |p| {
            out.push(Candidate { end: p.end, cost: cost(1.0 + p.weight), rank, tag: String::new() });
            rank += 1;
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut rank = 0;
        paths(text, start, self.punct, |p| {
            if rank == candidate.rank {
                candidate.tag = render(p);
            }
            rank += 1;
        });
    }
}

fn append_field(tag: &mut String, name: &str, value: &str) {
    if !tag.ends_with("{ ") {
        tag.push(' ');
    }
    tag.push_str(name);
    tag.push_str(": \"");
    for c in value.chars() {
        tag.push(if c == ' ' && matches!(name, "currency_maj" | "currency_min" | "quantity") { '\u{a0}' } else { c });
    }
    tag.push('"');
}

fn render(p: Path) -> String {
    let mut tag = String::from("money { ");
    if p.negative {
        append_field(&mut tag, "negative", "true");
    }
    let mut preserve = !p.prefix;
    match p.form {
        Form::Cents if p.number == 1 => {
            append_field(&mut tag, "currency_maj", "سنت");
            append_field(&mut tag, "integer_part", "واحد");
            append_field(&mut tag, "morphosyntactic_features", "currency_first");
            preserve = false;
        }
        Form::Cents if p.number == 2 => {
            append_field(&mut tag, "integer_part", "اثنان");
            append_field(&mut tag, "currency_maj", "سنت");
            preserve = true;
        }
        Form::Cents => {
            append_field(&mut tag, "fractional_part", &spell_cardinal(p.number));
            append_field(&mut tag, "currency_min", if p.number <= 10 { "سنتات" } else { "سنتا" });
            preserve = true;
        }
        Form::MinorOnly => {
            append_field(&mut tag, "fractional_part", &spell_cardinal(p.fraction));
            append_field(&mut tag, "currency_min", p.minor.unwrap());
            preserve = true;
        }
        form => {
            let spoken = match form {
                Form::Shorthand(mag, Some((frac, digits))) => {
                    let magnitude = match mag.to_ascii_lowercase() {
                        'k' => "ألف",
                        'm' => "مليون",
                        'b' => "مليار",
                        _ => "تريليون",
                    };
                    format!(
                        "{} و{} {} {}",
                        spell_cardinal(p.number),
                        spell_cardinal(frac),
                        if digits == 1 { "من عشرة" } else { "من مئة" },
                        magnitude
                    )
                }
                Form::Shorthand(mag, None) => match mag.to_ascii_lowercase() {
                    'k' => spell_cardinal(p.number * 1_000),
                    'm' => spell_cardinal(p.number * 1_000_000),
                    'b' => spell_cardinal(p.number * 1_000_000_000),
                    _ if p.number == 1 => "تريليون".to_owned(),
                    _ if p.number == 2 => "تريليونان".to_owned(),
                    _ if p.number <= 10 => format!("{} تريليونات", spell_cardinal(p.number)),
                    _ => format!("{} تريليون", spell_cardinal(p.number)),
                },
                _ => spell_cardinal(p.number),
            };
            let spoken = if matches!(form, Form::Shorthand(..)) { spoken.replace(' ', "\u{a0}") } else { spoken };
            let cf = matches!(form, Form::CurrencyFirst);
            let unpermuted_minor = matches!(form, Form::MinorWithoutUnit) && p.prefix;
            if cf
                || (p.prefix && !matches!(form, Form::Minor | Form::MinorFallback | Form::MinorWithoutUnit))
                || unpermuted_minor
            {
                append_field(&mut tag, "currency_maj", p.currency);
                append_field(&mut tag, "integer_part", &spoken);
            } else {
                append_field(&mut tag, "integer_part", &spoken);
                append_field(&mut tag, "currency_maj", p.currency);
            }
            if cf {
                append_field(&mut tag, "morphosyntactic_features", "currency_first");
            }
            if matches!(form, Form::Minor | Form::MinorWithoutUnit | Form::MinorFallback) {
                append_field(&mut tag, "fractional_part", &spell_cardinal(p.fraction));
                if let Some(minor) = p.minor {
                    append_field(&mut tag, "currency_min", minor);
                }
                preserve = matches!(form, Form::Minor | Form::MinorFallback) || !p.prefix;
            }
        }
    }
    if let Some(q) = p.quantity {
        append_field(&mut tag, "quantity", q);
    }
    if preserve {
        tag.push_str(" preserve_order: true");
    }
    tag.push_str(" }");
    tag
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        Some("true") => true,
        None => false,
        _ => return None,
    };
    let mut out = String::new();
    if neg {
        out.push_str("سالب ");
    }
    let currency_first = f.peek_key() == Some("currency_maj");
    if currency_first {
        let maj = f.take("currency_maj")?;
        let integer = f.take("integer_part")?;
        if maj.is_empty() || integer.is_empty() || f.take("morphosyntactic_features")? != "currency_first" {
            return None;
        }
        out.push_str(maj);
        out.push(' ');
        out.push_str(integer);
    } else if let Some(integer) = f.take("integer_part") {
        let maj = f.take("currency_maj")?;
        if integer.is_empty() || maj.is_empty() {
            return None;
        }
        out.push_str(integer);
        out.push(' ');
        out.push_str(maj);
        if let Some(frac) = f.take("fractional_part") {
            if frac.is_empty() {
                return None;
            }
            out.push_str(" و");
            out.push_str(frac);
            if let Some(min) = f.take("currency_min") {
                if min.is_empty() {
                    return None;
                }
                out.push(' ');
                out.push_str(min);
            }
        }
    } else {
        let frac = f.take("fractional_part")?;
        let min = f.take("currency_min")?;
        if frac.is_empty() || min.is_empty() {
            return None;
        }
        out.push_str(frac);
        out.push(' ');
        out.push_str(min);
    }
    if let Some(quantity) = f.take("quantity") {
        if quantity.is_empty() {
            return None;
        }
        out.push(' ');
        out.push_str(quantity);
    }
    if !currency_first {
        loop {
            if f.preserve_order() {
                continue;
            }
            if f.peek_key() == Some("field_order") {
                if f.take("field_order")?.chars().count() != 1 {
                    return None;
                }
                continue;
            }
            break;
        }
    }
    if !f.done() {
        return None;
    }
    Some(out.replace('\u{a0}', " "))
}
