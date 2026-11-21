//! Productive Urdu measure and address grammar.

use super::Sink;
use super::cardinal::{code_num, digitwise, enumerate_cardinal, enumerate_without_leading_zeros};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

fn prefix_len(text: &[char], key: &str) -> Option<usize> {
    let mut len = 0;
    for c in key.chars() {
        if text.get(len) != Some(&c) {
            return None;
        }
        len += 1;
    }
    Some(len)
}

static CITIES_BY_END: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    for city in STATES_DATA.lines().chain(CITIES_DATA.lines()).map(|s| s.trim_end_matches('\r')) {
        let chars: Vec<char> = city.chars().collect();
        if let Some(&c) = chars.last() {
            index.entry(c).or_default().push(chars);
        }
    }
    index
});

static ASCII_CITY_ENDS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut ends = [false; 128];
    for &c in CITIES_BY_END.keys() {
        if c.is_ascii() {
            ends[c as usize] = true;
        }
    }
    ends
});

static CONTEXT_KEYS: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    for &key in ADDRESS_KEYWORDS.iter() {
        let chars: Vec<char> = key.chars().collect();
        if let Some(&first) = chars.first() {
            index.entry(first).or_default().push(chars);
        }
    }
    index
});

const UNIT_DATA: &str = include_str!("../../../../data/ur/measure/unit.tsv");
const QUARTERLY_MAP_DATA: &str = include_str!("../../../../data/ur/measure/quarterly_units_map.tsv");
const QUARTERLY_LIST_DATA: &str = include_str!("../../../../data/ur/measure/quarterly_units_list.tsv");
const PAUNE_DATA: &str = include_str!("../../../../data/ur/whitelist/paune_mappings.tsv");

const STATES_DATA: &str = include_str!("../../../../data/ur/address/states.tsv");
const CITIES_DATA: &str = include_str!("../../../../data/ur/address/cities.tsv");
const CONTEXT_DATA: &str = include_str!("../../../../data/ur/address/context.tsv");

static UNITS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let mut v: Vec<(&'static str, &'static str)> = UNIT_DATA
        .lines()
        .filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
        .filter(|&(k, _)| k != "yr")
        .collect();
    v.sort_by(|a, b| b.0.len().cmp(&a.0.len()));
    v
});

static QUARTERLY_UNITS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let mut v: Vec<(&'static str, &'static str)> =
        QUARTERLY_MAP_DATA.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t')).collect();
    for l in QUARTERLY_LIST_DATA.lines() {
        let s = l.trim_end_matches('\r').trim();
        if !s.is_empty() {
            v.push((s, s));
        }
    }
    v.sort_by(|a, b| b.0.len().cmp(&a.0.len()));
    v
});

type UnitIndex = HashMap<char, Vec<(&'static str, &'static str)>>;
fn index_units(rows: &[(&'static str, &'static str)]) -> UnitIndex {
    let mut index: UnitIndex = HashMap::new();
    for &(key, value) in rows {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
}
static UNITS_BY_FIRST: LazyLock<UnitIndex> = LazyLock::new(|| index_units(&UNITS));
static QUARTERLY_BY_FIRST: LazyLock<UnitIndex> = LazyLock::new(|| index_units(&QUARTERLY_UNITS));

static UNITS_BY_END: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    for key in UNITS.iter().chain(QUARTERLY_UNITS.iter()).map(|(key, _)| *key).chain(std::iter::once("yr")) {
        let chars: Vec<char> = key.chars().collect();
        if let Some(&last) = chars.last() {
            index.entry(last).or_default().push(chars);
        }
    }
    index
});

static ASCII_UNIT_ENDS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut ends = [false; 128];
    for &c in UNITS_BY_END.keys() {
        if c.is_ascii() {
            ends[c as usize] = true;
        }
    }
    ends
});
static CONTEXT_BY_END: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    for &key in ADDRESS_KEYWORDS.iter() {
        let chars: Vec<char> = key.chars().collect();
        if let Some(&last) = chars.last() {
            index.entry(last).or_default().push(chars);
        }
    }
    index
});

static ASCII_CONTEXT_ENDS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut ends = [false; 128];
    for &c in CONTEXT_BY_END.keys() {
        if c.is_ascii() {
            ends[c as usize] = true;
        }
    }
    ends
});

pub(crate) fn context_ends_at(span: &[char]) -> bool {
    if span.last().is_some_and(|c| c.is_ascii() && !ASCII_CONTEXT_ENDS[*c as usize]) {
        return false;
    }
    span.last().and_then(|c| CONTEXT_BY_END.get(c)).is_some_and(|keys| keys.iter().any(|key| span.ends_with(key)))
}

/// Every address path requires a source context keyword or city/state.
pub(crate) fn address_ends_at(span: &[char]) -> bool {
    context_ends_at(span) || ends_in_city(span)
}

/// Check the first whitespace after a numeric measure against the source
/// unit/range continuations. Address paths are handled independently.
pub(crate) fn numeric_space_can_continue(rest: &[char], percent_before_number: bool) -> bool {
    let skipped = rest.iter().take_while(|&&c| crate::text::is_nemo_space(c)).count();
    let rest = &rest[skipped..];
    if percent_before_number && matches!(rest.first(), Some('-') | Some('0'..='9')) {
        return true;
    }
    if matches!(rest.first(), Some('-' | '–')) || rest.starts_with(&['y', 'r']) {
        return true;
    }
    rest.first().is_some_and(|c| {
        UNITS_BY_FIRST
            .get(c)
            .into_iter()
            .flatten()
            .chain(QUARTERLY_BY_FIRST.get(c).into_iter().flatten())
            .any(|(key, _)| prefix_len(rest, key).is_some())
    })
}

fn ends_in_city(span: &[char]) -> bool {
    if span.last().is_some_and(|c| c.is_ascii() && !ASCII_CITY_ENDS[*c as usize]) {
        return false;
    }
    span.last().and_then(|c| CITIES_BY_END.get(c)).is_some_and(|cities| cities.iter().any(|city| span.ends_with(city)))
}

pub(crate) fn possible_span(span: &[char], has_context: bool, numeric_live: bool, address_future: bool) -> bool {
    if has_context {
        return true;
    }
    let Some((&first, &last)) = span.first().zip(span.last()) else { return false };
    let numeric = first.is_ascii_digit() || matches!(first, '-' | '%' | '٪');
    if numeric_live && numeric {
        if last.is_ascii_digit() && (matches!(first, '%' | '٪') || span.iter().any(|c| matches!(c, 'x' | 'X' | '*'))) {
            return true;
        }
        if (!last.is_ascii() || ASCII_UNIT_ENDS[last as usize])
            && UNITS_BY_END.get(&last).is_some_and(|keys| keys.iter().any(|key| span.ends_with(key)))
        {
            return true;
        }
    }
    if !address_future {
        return false;
    }
    if ends_in_city(span) {
        return true;
    }
    span.len() > 7
        && span[span.len() - 7] == ' '
        && span[span.len() - 6..].iter().all(char::is_ascii_digit)
        && ends_in_city(&span[..span.len() - 7])
}

static PAUNE_MAP: LazyLock<[Option<&'static str>; 100]> = LazyLock::new(|| {
    let mut out = [None; 100];
    for line in PAUNE_DATA.lines() {
        let Some((k, v)) = line.trim_end_matches('\r').split_once('\t') else { continue };
        if let Ok(n) = k.parse::<usize>() {
            if n < 100 {
                out[n] = Some(v);
            }
        }
    }
    out
});

static ADDRESS_KEYWORDS: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    let mut v = Vec::new();
    for l in CONTEXT_DATA.lines() {
        let s = l.trim_end_matches('\r').trim();
        if !s.is_empty() {
            v.push(s);
        }
    }
    v.sort_by(|a, b| b.len().cmp(&a.len()));
    v
});

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    classify_with_address(text, start, sink, true);
}

pub(crate) fn classify_with_address(text: &[char], start: usize, sink: &mut Sink, address_possible: bool) {
    if start >= text.len() {
        return;
    }

    // 1. Prefix percent: "%10" or "٪10" (weight 0.1)
    if text[start] == '%' || text[start] == '٪' {
        let mut num_start = start + 1;
        while num_start < text.len() && crate::text::is_nemo_space(text[num_start]) {
            num_start += 1;
        }
        let (neg, n_start) =
            if num_start < text.len() && text[num_start] == '-' { (true, num_start + 1) } else { (false, num_start) };
        if n_start < text.len() && text[n_start].is_ascii_digit() {
            let mut num_end = n_start;
            while num_end < text.len() && (text[num_end].is_ascii_digit() || text[num_end] == ',') {
                num_end += 1;
            }
            let int_chars = &text[n_start..num_end];
            // Check decimal: "% 10.5"
            if num_end < text.len() && text[num_end] == '.' {
                let frac_start = num_end + 1;
                let mut frac_end = frac_start;
                while frac_end < text.len() && text[frac_end].is_ascii_digit() {
                    frac_end += 1;
                }
                if frac_end > frac_start {
                    let frac_spoken = crate::lang::ur::lazy::tag(|| {
                        let frac_str: String = text[frac_start..frac_end].iter().collect();
                        digitwise(&frac_str).unwrap()
                    });
                    {
                        enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                            let c = cost(1.1) + cost(0.1) + int_c;
                            sink.push(frac_end, c, 0, || {
                                format!(
                                    "measure {{ units: \"فیصد\" decimal {{ {}integer_part: \"{}\" fractional_part: \"{}\" }} }}",
                                    if neg { "negative: \"true\" " } else { "" },
                                    int_s,
                                    frac_spoken
                                )
                            });
                        });
                    }
                }
            } else {
                enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                    let c = cost(1.1) + cost(0.1) + int_c;
                    sink.push(num_end, c, 0, move || {
                        format!(
                            "measure {{ units: \"فیصد\" cardinal {{ {}integer: \"{}\" }} }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_s
                        )
                    });
                });
            }
        }
    }

    let (neg, s_start) = if text[start] == '-' { (true, start + 1) } else { (false, start) };
    if s_start >= text.len() {
        return;
    }
    let remaining = &text[s_start..];

    // Check if starts with digits
    let mut num_end = 0;
    while num_end < remaining.len() && (remaining[num_end].is_ascii_digit() || remaining[num_end] == ',') {
        num_end += 1;
    }

    if num_end > 0 {
        let int_chars = &remaining[..num_end];
        let after_int = &remaining[num_end..];

        // Dimension exception: the symbol is consumed without input spaces.
        let mut sym_len = 0;
        let after_sym_lead = after_int;
        if after_sym_lead.starts_with(&['x'])
            || after_sym_lead.starts_with(&['X'])
            || after_sym_lead.starts_with(&['*'])
        {
            sym_len = 1;
        }
        if sym_len > 0 {
            let after_sym_pos = sym_len;
            let rem_num2 = &after_int[after_sym_pos..];
            let (neg2, n2_start) = if rem_num2.starts_with(&['-']) { (true, 1) } else { (false, 0) };
            let rem_num2_digits = &rem_num2[n2_start..];
            let mut num2_end = 0;
            while num2_end < rem_num2_digits.len()
                && (rem_num2_digits[num2_end].is_ascii_digit() || rem_num2_digits[num2_end] == ',')
            {
                num2_end += 1;
            }
            if num2_end > 0 {
                let num2_chars = &rem_num2_digits[..num2_end];
                let end = s_start + num_end + after_sym_pos + n2_start + num2_end;
                enumerate_cardinal(int_chars, &mut |s1, c1| {
                    enumerate_cardinal(num2_chars, &mut |s2, c2| {
                        let c = cost(1.1) + cost(0.1) + c1 + c2;
                        sink.push(end, c, 0, || {
                            format!(
                                "measure {{ cardinal {{ {}integer: \"{}\" }} units: \"بائی\" }} }} tokens {{ cardinal {{ {}integer: \"{}\" }}",
                                if neg { "negative: \"true\" " } else { "" },
                                s1,
                                if neg2 { "negative: \"true\" " } else { "" },
                                s2
                            )
                        });
                    });
                });
            }
        }

        // 3. Quarterly units: dedh (1.5), dhai (2.5), savva (.25), sadhe (.5), paune (.75)
        if after_int.starts_with(&['.']) && after_int.get(1).is_some_and(|c| c.is_ascii_digit()) {
            let frac_rem = &after_int[1..];
            // dedh: 1.5
            if num_end == 1
                && int_chars[0] == '1'
                && frac_rem.starts_with(&['5'])
                && (frac_rem.len() == 1 || !frac_rem[1].is_ascii_digit())
            {
                let after_frac = &frac_rem[1..];
                classify_quarterly_tail(s_start + 3, after_frac, "ڈیڑھ", cost(-0.2), neg, sink);
            }
            // dhai: 2.5
            if num_end == 1
                && int_chars[0] == '2'
                && frac_rem.starts_with(&['5'])
                && (frac_rem.len() == 1 || !frac_rem[1].is_ascii_digit())
            {
                let after_frac = &frac_rem[1..];
                classify_quarterly_tail(s_start + 3, after_frac, "ڈھائی", cost(-0.2), neg, sink);
            }
            // savva: .25
            if frac_rem.starts_with(&['2', '5']) && (frac_rem.len() == 2 || !frac_rem[2].is_ascii_digit()) {
                let after_frac = &frac_rem[2..];
                let base_len = s_start + num_end + 3;
                enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                    let phrase = crate::lang::ur::lazy::tag(|| format!("سوا {int_s}"));
                    classify_quarterly_tail(base_len, after_frac, &phrase, cost(-0.1) + int_c, neg, sink);
                });
            }
            // sadhe: .5 (except 1.5, 2.5)
            if !(num_end == 1 && (int_chars[0] == '1' || int_chars[0] == '2'))
                && frac_rem.starts_with(&['5'])
                && (frac_rem.len() == 1 || !frac_rem[1].is_ascii_digit())
            {
                let after_frac = &frac_rem[1..];
                let base_len = s_start + num_end + 2;
                enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                    let phrase = crate::lang::ur::lazy::tag(|| format!("ساڑھے {int_s}"));
                    classify_quarterly_tail(base_len, after_frac, &phrase, cost(-0.1) + int_c, neg, sink);
                });
            }
            // paune: .75
            if frac_rem.starts_with(&['7', '5']) && (frac_rem.len() == 2 || !frac_rem[2].is_ascii_digit()) {
                let after_frac = &frac_rem[2..];
                let num_str: String = int_chars.iter().collect();
                if (num_str.len() == 1 || !num_str.starts_with('0'))
                    && let Ok(n) = num_str.parse::<usize>()
                {
                    if n < 100 {
                        if let Some(paune_word) = PAUNE_MAP[n] {
                            let phrase = crate::lang::ur::lazy::tag(|| format!("پونے {paune_word}"));
                            classify_quarterly_tail(s_start + num_end + 3, after_frac, &phrase, cost(-0.5), neg, sink);
                        }
                    }
                }
            }

            // Decimal + regular unit: "12.5kg", "12.5 kg"
            let mut f_end = 1;
            while f_end < frac_rem.len() && frac_rem[f_end].is_ascii_digit() {
                f_end += 1;
            }
            let frac_chars = &frac_rem[..f_end];
            let frac_spoken = crate::lang::ur::lazy::tag(|| {
                let frac_str: String = frac_chars.iter().collect();
                digitwise(&frac_str).unwrap()
            });
            {
                let after_dec = &frac_rem[f_end..];
                let sp_off = after_dec.iter().take_while(|&&c| crate::text::is_nemo_space(c)).count();
                let after_dec_sp = &after_dec[sp_off..];

                // Check "yr" -> "سال"
                if after_dec_sp.starts_with(&['y', 'r']) {
                    let end = s_start + num_end + 1 + f_end + sp_off + 2;
                    enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                        let c = cost(1.1) + cost(0.1) + int_c;
                        sink.push(end, c, 0, || {
                            format!(
                                "measure {{ decimal {{ {}integer_part: \"{}\" fractional_part: \"{}\" }} units: \"سال\" }}",
                                if neg { "negative: \"true\" " } else { "" },
                                int_s,
                                frac_spoken
                            )
                        });
                    });
                }

                // Check regular units
                for &(u_code, u_spoken) in
                    after_dec_sp.first().and_then(|c| UNITS_BY_FIRST.get(c)).into_iter().flatten()
                {
                    if let Some(u_len) = prefix_len(after_dec_sp, u_code) {
                        let end = s_start + num_end + 1 + f_end + sp_off + u_len;
                        enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                            let c = cost(1.1) + cost(0.1) + int_c;
                            sink.push(end, c, 0, || {
                                format!(
                                    "measure {{ decimal {{ {}integer_part: \"{}\" fractional_part: \"{}\" }} units: \"{}\" }}",
                                    if neg { "negative: \"true\" " } else { "" },
                                    int_s,
                                    frac_spoken,
                                    u_spoken
                                )
                            });
                        });
                    }
                }
            }
        }

        // 4. Cardinal + regular unit: "12kg", "12 kg", "12 yr"
        let sp_off = after_int.iter().take_while(|&&c| crate::text::is_nemo_space(c)).count();
        let after_sp = &after_int[sp_off..];

        // Year formal (>= 1000) vs informal (< 1000)
        if after_sp.starts_with(&['y', 'r']) {
            let end = s_start + num_end + sp_off + 2;
            let weight_offset = if num_end >= 4 { cost(0.1) } else { cost(-0.1) };
            enumerate_without_leading_zeros(int_chars, &mut |int_s, int_c| {
                let c = cost(1.1) + weight_offset + int_c;
                sink.push(end, c, 0, move || {
                    format!(
                        "measure {{ cardinal {{ {}integer: \"{}\" }} units: \"سال\" }}",
                        if neg { "negative: \"true\" " } else { "" },
                        int_s
                    )
                });
            });
        }

        // Regular units
        for &(u_code, u_spoken) in after_sp.first().and_then(|c| UNITS_BY_FIRST.get(c)).into_iter().flatten() {
            if let Some(u_len) = prefix_len(after_sp, u_code) {
                // Exclude decades: "1990s"
                if u_code == "s"
                    && !neg
                    && sp_off == 0
                    && num_end == 4
                    && (int_chars[0] == '1' || int_chars[0] == '2')
                    && int_chars[3] == '0'
                {
                    continue;
                }
                let end = s_start + num_end + sp_off + u_len;
                enumerate_cardinal(int_chars, &mut |int_s, int_c| {
                    let c = cost(1.1) + cost(0.1) + int_c;
                    sink.push(end, c, 0, move || {
                        format!(
                            "measure {{ cardinal {{ {}integer: \"{}\" }} units: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_s,
                            u_spoken
                        )
                    });
                });
            }
        }
    }

    classify_range(text, start, sink);
    // 6. Address / Structured Address
    if address_possible {
        classify_address(text, start, sink);
    }
}

fn classify_quarterly_tail(
    base_pos: usize,
    after_frac: &[char],
    phrase: &str,
    base_cost: Cost,
    neg: bool,
    sink: &mut Sink,
) {
    let sp_off = after_frac.iter().take_while(|&&c| crate::text::is_nemo_space(c)).count();
    let after_sp = &after_frac[sp_off..];
    for &(u_code, u_spoken) in after_sp.first().and_then(|c| QUARTERLY_BY_FIRST.get(c)).into_iter().flatten() {
        if let Some(u_len) = prefix_len(after_sp, u_code) {
            let end = base_pos + sp_off + u_len;
            let c = cost(1.1) + base_cost;
            let phrase = crate::lang::ur::lazy::tag(|| phrase.to_owned());
            sink.push(end, c, 0, move || {
                format!(
                    "measure {{ cardinal {{ {}integer: \"{}\" }} units: \"{}\" }}",
                    if neg { "negative: \"true\" " } else { "" },
                    phrase,
                    u_spoken
                )
            });
        }
    }
}

fn classify_address(text: &[char], start: usize, sink: &mut Sink) {
    let remaining = &text[start..];

    // Structured addresses consist of up to five text/number tokens, optionally
    // preceded by a street number, followed by a city or state and optional PIN.
    let pin_start = remaining.iter().rposition(|&c| c == ' ');
    let last = remaining.last().copied();
    let pin_last = pin_start.and_then(|pos| {
        let pin = &remaining[pos + 1..];
        (pin.len() == 6 && pin.iter().all(char::is_ascii_digit))
            .then(|| pos.checked_sub(1).and_then(|i| remaining.get(i)).copied())
            .flatten()
    });
    let city_matches = |end: usize| ends_in_city(&remaining[..end]);
    if last.is_some_and(|_| city_matches(remaining.len())) || pin_last.is_some_and(|_| city_matches(pin_start.unwrap()))
    {
        let input: String = remaining.iter().collect();
        for city in STATES_DATA.lines().chain(CITIES_DATA.lines()).map(|s| s.trim_end_matches('\r')) {
            if city.is_empty() {
                continue;
            }
            for has_pin in [false, true] {
                let (without_pin, pin) = if has_pin {
                    let Some((before, pin)) = input.rsplit_once(' ') else { continue };
                    if pin.len() != 6 || !pin.bytes().all(|c| c.is_ascii_digit()) {
                        continue;
                    }
                    (before, Some(pin))
                } else {
                    (input.as_str(), None)
                };
                let Some(before_city) = without_pin.strip_suffix(city) else { continue };
                let mut weight = cost(2.1);
                let mut valid = true;
                let mut count = 0;
                let mut first_numeric = false;
                for token in before_city.split_inclusive(' ') {
                    if !token.ends_with(' ') {
                        valid = false;
                        break;
                    }
                    let raw = &token[..token.len() - 1];
                    let word = raw.strip_suffix(',').unwrap_or(raw);
                    if word.is_empty() {
                        valid = false;
                        break;
                    }
                    let numeric = word.bytes().all(|c| c.is_ascii_digit());
                    if count == 0 {
                        first_numeric = numeric;
                    }
                    count += 1;
                    if numeric {
                        if let Some((_, w)) = code_num(word) {
                            weight += w;
                        } else {
                            valid = false;
                            break;
                        }
                    } else if word.chars().any(|c| {
                        c.is_ascii_digit() || crate::text::is_nemo_space(c) || matches!(c, ',' | '،' | '.' | '۔')
                    }) {
                        valid = false;
                        break;
                    }
                }
                if !valid || count > 6 || (count == 6 && !first_numeric) {
                    continue;
                }
                let end = text.len();
                sink.push(end, weight, 0, || {
                    let mut spoken = String::new();
                    for token in before_city.split_inclusive(' ') {
                        let raw = &token[..token.len() - 1];
                        let word = raw.strip_suffix(',').unwrap_or(raw);
                        if word.bytes().all(|c| c.is_ascii_digit()) {
                            if let Some((value, _)) = code_num(word) {
                                spoken.push_str(&value);
                            }
                        } else {
                            spoken.push_str(word);
                        }
                        if raw.ends_with(',') {
                            spoken.push(',');
                        }
                        spoken.push(' ');
                    }
                    spoken.push_str(city);
                    if let Some(pin) = pin {
                        spoken.push(' ');
                        if let Some(value) = digitwise(pin) {
                            spoken.push_str(&value);
                        }
                    }
                    format!(
                        "measure {{ units: \"address\" cardinal {{ integer: \"{}\" }} preserve_order: true }}",
                        spoken
                    )
                });
            }
        }
    }

    classify_context_address(remaining, text.len(), sink);
}

fn range_number(s: &str, emit: &mut dyn FnMut(String, Cost)) {
    let (integer, fraction) = match s.split_once('.') {
        Some((i, f)) if !f.is_empty() && f.bytes().all(|b| b.is_ascii_digit()) => (i, Some(f)),
        Some(_) => return,
        None => (s, None),
    };
    let chars: Vec<char> = integer.chars().collect();
    enumerate_cardinal(&chars, &mut |spoken, weight| {
        if let Some(fraction) = fraction {
            if let Some(f) = digitwise(fraction) {
                emit(crate::lang::ur::lazy::tag(|| format!("{spoken} اعشاریہ {f}")), weight);
            }
        } else {
            emit(spoken, weight);
        }
    });
}

fn classify_range(text: &[char], start: usize, sink: &mut Sink) {
    if !text[start..].iter().any(|&c| matches!(c, '-' | '–')) {
        return;
    }
    let input: String = text[start..].iter().collect();
    for &(unit, spoken_unit) in UNITS.iter() {
        let Some(numbers) = input.strip_suffix(unit) else { continue };
        let numbers = numbers.trim_end_matches(crate::text::is_nemo_space);
        for separator in [" - ", " – ", "-", "–"] {
            let Some((left, right)) = numbers.split_once(separator) else { continue };
            range_number(left, &mut |a, a_cost| {
                range_number(right, &mut |b, b_cost| {
                    sink.push(text.len(), cost(1.05) + a_cost + b_cost, 0, || {
                        format!("measure {{ cardinal {{ integer: \"{} سے {}\" }} units: \"{}\" }}", a, b, spoken_unit)
                    });
                });
            });
        }
    }
}

#[derive(Clone, Copy)]
enum AddressArc {
    Space,
    Comma,
    Symbol(char),
    Letter(char),
    Identity(usize, usize),
    Number(usize, usize),
    Ordinal(usize, usize, Cost),
    Mixed(usize, usize),
}

#[derive(Clone, Copy)]
struct AddressPath {
    weight: Cost,
    previous: usize,
    arc: AddressArc,
}

fn address_boundary(c: char) -> bool {
    crate::text::is_nemo_space(c) || matches!(c, ',' | '،' | '.' | '۔')
}

fn address_window(s: &[char], left: bool) -> bool {
    if s.is_empty() {
        return true;
    }
    if left && address_boundary(s[0]) && s.iter().any(|&c| !address_boundary(c)) {
        return false;
    }
    let mut words = 0;
    let mut in_word = false;
    for &c in s {
        if address_boundary(c) {
            in_word = false;
        } else if !in_word {
            words += 1;
            in_word = true;
        }
    }
    words <= 5
}

fn address_context(input: &[char]) -> bool {
    for pos in 0..input.len() {
        let Some(keys) = CONTEXT_KEYS.get(&input[pos]) else { continue };
        for key in keys {
            if !input[pos..].starts_with(key) {
                continue;
            }
            let left = &input[..pos];
            let right = &input[pos + key.len()..];
            let left_ok = !left.is_empty() && address_boundary(left[left.len() - 1]) && address_window(left, true);
            let right_ok = !right.is_empty() && address_boundary(right[0]) && address_window(right, false);
            if (left.is_empty() && right_ok) || (left_ok && (right.is_empty() || right_ok)) {
                return true;
            }
        }
    }
    false
}

fn address_push(paths: &mut [Option<AddressPath>], from: usize, to: usize, weight: Cost, arc: AddressArc) {
    let Some(path) = paths[from] else { return };
    let weight = path.weight + weight;
    if paths[to].is_none_or(|old| weight < old.weight) {
        paths[to] = Some(AddressPath { weight, previous: from, arc });
    }
}

fn classify_context_address(input: &[char], end: usize, sink: &mut Sink) {
    if !address_context(input) {
        return;
    }
    let mut paths = vec![None; input.len() + 1];
    paths[0] = Some(AddressPath { weight: 0, previous: 0, arc: AddressArc::Space });
    for i in 0..input.len() {
        if paths[i].is_none() {
            continue;
        }
        let c = input[i];
        if c == ' ' {
            address_push(&mut paths, i, i + 1, 0, AddressArc::Space);
        }
        let mut comma = i;
        while comma < input.len() && crate::text::is_nemo_space(input[comma]) {
            comma += 1;
        }
        if input.get(comma) == Some(&',') {
            address_push(&mut paths, i, comma + 1, 0, AddressArc::Comma);
        }
        if matches!(c, '-' | '/') {
            address_push(&mut paths, i, i + 1, 0, AddressArc::Symbol(c));
        }
        // mixed_alphanum_graph is composed before the serial tagger's exclusions.
        let mut has_digit = false;
        let mut has_letter = false;
        for j in i + 1..=input.len() {
            let ch = input[j - 1];
            if !(ch.is_ascii_alphanumeric()
                || matches!(ch, '-' | '/')
                || crate::lang::ur::graph_utils::is_urdu_char(ch))
            {
                break;
            }
            has_digit |= ch.is_ascii_digit();
            has_letter |= ch.is_ascii_alphabetic() || crate::lang::ur::graph_utils::is_urdu_char(ch);
            if has_digit && has_letter {
                let part: String = input[i..j].iter().collect();
                if let Some((_, w)) = super::serial::serial_core(&part) {
                    address_push(&mut paths, i, j, w, AddressArc::Mixed(i, j));
                }
            }
        }
        if c.is_ascii_digit() {
            for j in i + 1..=(i + 8).min(input.len()) {
                super::ordinal::enumerate_graph(&input[i..j], &mut |_, w| {
                    address_push(&mut paths, i, j, w + cost(-0.0001), AddressArc::Ordinal(i, j, w));
                });
            }
            for j in i + 1..=input.len() {
                if !input[j - 1].is_ascii_digit() {
                    break;
                }
                let part = &input[i..j];
                let s: String = part.iter().collect();
                if let Some((_, w)) = code_num(&s) {
                    address_push(&mut paths, i, j, cost(0.5) + w, AddressArc::Number(i, j));
                }
            }
        }
        if c.is_ascii_alphabetic() {
            address_push(&mut paths, i, i + 1, 0, AddressArc::Letter(c));
            for j in i + 2..=input.len() {
                if !input[j - 1].is_ascii_alphabetic() {
                    break;
                }
                address_push(&mut paths, i, j, 0, AddressArc::Identity(i, j));
            }
        }
        if !crate::text::is_nemo_space(c) && !c.is_ascii_alphanumeric() && !matches!(c, '-' | '/' | ',') {
            for j in i + 1..=input.len() {
                let ch = input[j - 1];
                if crate::text::is_nemo_space(ch) || ch.is_ascii_alphanumeric() || matches!(ch, '-' | '/' | ',') {
                    break;
                }
                address_push(&mut paths, i, j, cost(0.1), AddressArc::Identity(i, j));
            }
        }
    }
    let Some(final_path) = paths[input.len()] else { return };
    sink.push(end, cost(1.1) + final_path.weight, 0, || {
        let mut arcs = Vec::new();
        let mut pos = input.len();
        while pos != 0 {
            let path = paths[pos].unwrap();
            arcs.push(path.arc);
            pos = path.previous;
        }
        let mut result = String::new();
        for arc in arcs.iter().rev() {
            match *arc {
                AddressArc::Space => result.push(' '),
                AddressArc::Comma => result.push(','),
                AddressArc::Symbol(c) => {
                    result.push(' ');
                    result.push_str(if c == '-' { "ہائفن" } else { "بٹا" });
                }
                AddressArc::Letter(c) => {
                    result.push(' ');
                    result.push_str(super::serial::letter_to_spoken(c).unwrap());
                }
                AddressArc::Identity(a, b) => {
                    result.push(' ');
                    result.extend(input[a..b].iter());
                }
                AddressArc::Number(a, b) => {
                    let text: String = input[a..b].iter().collect();
                    result.push(' ');
                    result.push_str(&code_num(&text).unwrap().0);
                }
                AddressArc::Mixed(a, b) => {
                    let text: String = input[a..b].iter().collect();
                    result.push(' ');
                    result.push_str(&super::serial::serial_core(&text).unwrap().0);
                }
                AddressArc::Ordinal(a, b, target) => {
                    let mut value = None;
                    super::ordinal::enumerate_graph(&input[a..b], &mut |spoken, w| {
                        if w == target && value.is_none() {
                            value = Some(spoken);
                        }
                    });
                    result.push(' ');
                    result.push_str(&value.unwrap());
                }
            }
        }
        format!("measure {{ units: \"address\" cardinal {{ integer: \"{}\" }} preserve_order: true }}", result)
    });
}
