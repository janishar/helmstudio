//! Bengali Money classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const CURRENCY_DATA: &str = include_str!("../../../data/bn/money/currency.tsv");
const MAJ_MIN_DATA: &str = include_str!("../../../data/bn/money/major_minor_currencies.tsv");
const QUANTITIES_DATA: &str = include_str!("../../../data/bn/numbers/quantities.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static CURRENCIES: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let mut v: Vec<(&'static str, &'static str)> = rows(CURRENCY_DATA).collect();
    v.sort_by(|a, b| b.0.len().cmp(&a.0.len()));
    v
});

type CurrencyIndex = std::collections::HashMap<char, Vec<(&'static str, &'static str)>>;

static CURRENCIES_BY_FIRST: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index = CurrencyIndex::new();
    for &(key, value) in CURRENCIES.iter() {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
});

static CURRENCIES_BY_LAST: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index = CurrencyIndex::new();
    for &(key, value) in CURRENCIES.iter() {
        if let Some(c) = key.chars().next_back() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
});

static MAJ_TO_MIN: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(MAJ_MIN_DATA).collect());

static QUANTITIES: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| QUANTITIES_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static CURRENCY_CHARS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars: Vec<char> = CURRENCIES.iter().flat_map(|(key, _)| key.chars()).collect();
    chars.sort_unstable();
    chars.dedup();
    chars
});

fn currency_char(c: char) -> bool {
    CURRENCY_CHARS.binary_search(&c).is_ok()
}

const PER_UNIT_MAP: &[(&str, &str)] = &[
    ("kWh", "প্রতি কিলোওয়াট ঘন্টা"),
    ("month", "প্রতি মাস"),
    ("year", "প্রতি বছর"),
    ("min", "প্রতি মিনিট"),
    ("sec", "প্রতি সেকেন্ড"),
    ("day", "প্রতিদিন"),
    ("ft²", "প্রতি বর্গ ফুট"),
    ("ft2", "প্রতি বর্গ ফুট"),
    ("m²", "প্রতি বর্গ মিটার"),
    ("m2", "প্রতি বর্গ মিটার"),
    ("gal", "প্রতি গ্যালন"),
    ("hr", "প্রতি ঘণ্টা"),
    ("wk", "প্রতি সপ্তাহ"),
    ("mo", "প্রতি মাস"),
    ("yr", "প্রতি বছর"),
    ("kg", "প্রতি কিলোগ্রাম"),
    ("lb", "প্রতি পাউন্ড"),
    ("oz", "প্রতি আউন্স"),
    ("km", "প্রতি কিলোমিটার"),
    ("ft", "প্রতি ফুট"),
    ("ac", "প্রতি একর"),
    ("h", "প্রতি ঘণ্টা"),
    ("s", "প্রতি সেকেন্ড"),
    ("d", "প্রতিদিন"),
    ("g", "প্রতি গ্রাম"),
    ("t", "প্রতি টন"),
    ("l", "প্রতি লিটার"),
    ("L", "প্রতি লিটার"),
    ("m", "প্রতি মিটার"),
];

const MAG_SHORTHAND: &[(&str, &str)] =
    &[("k", "হাজার"), ("K", "হাজার"), ("M", "মিলিয়ন"), ("B", "বিলিয়ন"), ("T", "ট্রিলিয়ন")];

pub fn possible_start(c: char) -> bool {
    numbers::is_digit(c) || matches!(c, '-' | '−' | '~' | '±') || currency_char(c) || c.is_ascii_alphabetic()
}

pub fn has_currency_prefix(s: &str) -> bool {
    let clean = s.trim_start_matches(|c| matches!(c, '-' | '−' | '~' | '±'));
    clean
        .chars()
        .next()
        .and_then(|c| CURRENCIES_BY_FIRST.get(&c))
        .is_some_and(|entries| entries.iter().any(|(sym, _)| clean.starts_with(sym)))
}

pub fn currency_ending(s: &str, c: char) -> bool {
    currency_char(c)
        && CURRENCIES_BY_LAST.get(&c).is_some_and(|entries| entries.iter().any(|(key, _)| s.ends_with(key)))
}

pub fn possible_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(c, '-' | '−' | '~' | '±' | '.' | ',' | ' ' | '/' | '–' | '—')
        || ('\u{0980}'..='\u{09FF}').contains(&c)
        || c.is_ascii_alphanumeric()
        || currency_char(c)
}

pub fn possible_end(c: char) -> bool {
    numbers::is_digit(c)
        || c.is_ascii_alphanumeric()
        || currency_char(c)
        || c == '²'
        || ('\u{0980}'..='\u{09FF}').contains(&c)
}

pub fn matching_suffix(s: &str, _c: char) -> bool {
    s.chars()
        .next_back()
        .and_then(|c| CURRENCIES_BY_LAST.get(&c))
        .is_some_and(|entries| entries.iter().any(|(sym, _)| s.ends_with(sym)))
        || PER_UNIT_MAP.iter().any(|(u, _)| s.ends_with(u))
        || MAG_SHORTHAND.iter().any(|(m, _)| s.ends_with(m))
        || QUANTITIES.iter().any(|q| s.ends_with(q))
}

fn parse_sign(s: &str) -> (Option<&'static str>, &str) {
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (Some("true"), rest)
    } else if let Some(rest) = s.strip_prefix('~') {
        (Some("approx"), rest.trim_start_matches(' '))
    } else if let Some(rest) = s.strip_prefix('±') {
        (Some("pm"), rest.trim_start_matches(' '))
    } else {
        (None, s)
    }
}

fn parse_per_unit(s: &str) -> (&str, Option<&'static str>) {
    for &(unit_key, unit_spoken) in PER_UNIT_MAP {
        if let Some(prefix) = s.strip_suffix(unit_key) {
            if let Some(prefix) = prefix.strip_suffix('/').or_else(|| prefix.strip_suffix("/ ")) {
                return (prefix.trim_end_matches(' '), Some(unit_spoken));
            }
        }
    }
    (s, None)
}

fn parse_currency_prefix(s: &str) -> Option<(&'static str, &str)> {
    for &(sym, maj) in s.chars().next().and_then(|c| CURRENCIES_BY_FIRST.get(&c)).into_iter().flatten() {
        if let Some(rest) = s.strip_prefix(sym) {
            // Strip optional space or dot: "Tk 50", "Tk. 500", "Rs. 500"
            let rest = rest.strip_prefix(". ").or_else(|| rest.strip_prefix('.')).unwrap_or(rest);
            let rest = rest.trim_start_matches(' ');
            return Some((maj, rest));
        }
    }
    None
}

fn parse_currency_suffix(s: &str) -> Option<(&str, &'static str)> {
    for &(sym, maj) in s.chars().next_back().and_then(|c| CURRENCIES_BY_LAST.get(&c)).into_iter().flatten() {
        if let Some(prefix) = s.strip_suffix(sym) {
            let prefix = prefix.trim_end_matches(' ');
            return Some((prefix, maj));
        }
    }
    None
}

fn strip_trailing_zero_dot(s: &str) -> &str {
    if let Some(prefix) = s.strip_suffix(".00").or_else(|| s.strip_suffix(".০০")) {
        prefix
    } else if let Some(prefix) = s.strip_suffix(".0").or_else(|| s.strip_suffix(".০")) {
        prefix
    } else {
        s
    }
}

fn two_digit_fraction_to_cardinal(frac_str: &str) -> Option<String> {
    // Normalise to 2 digits: "5" -> "50", "50" -> "50"
    let frac_ascii: String = frac_str
        .chars()
        .map(|c| if ('০'..='৯').contains(&c) { (b'0' + (c as u32 - '০' as u32) as u8) as char } else { c })
        .collect();

    let frac_norm = if frac_ascii.len() == 1 {
        format!("{frac_ascii}0")
    } else if frac_ascii.len() == 2 {
        frac_ascii
    } else {
        return None;
    };

    let stripped = frac_norm.trim_start_matches('0');
    if stripped.is_empty() {
        return None;
    }
    numbers::graph(stripped).into_iter().next().map(|r| r.0)
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    // 1. Repeated currency range: $10-$20 -> tokens { money { ... } } tokens { name: "থেকে" } tokens { money { ... } }
    for dash in ["-", "–", "—"] {
        if let Some((left, right)) = s.split_once(dash) {
            let left = left.trim();
            let right = right.trim();
            let mut left_candidates = Vec::new();
            let mut right_candidates = Vec::new();
            classify_single(left, end, &mut left_candidates, false);
            classify_single(right, end, &mut right_candidates, false);
            for l_cand in &left_candidates {
                for r_cand in &right_candidates {
                    let l_tag = l_cand.tag.clone();
                    let r_tag = r_cand.tag.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.01) + l_cand.cost + r_cand.cost,
                        rank: 7,
                        tag: super::lazy::tag(move || {
                            format!("{l_tag} }} tokens {{ name: \"থেকে\" }} tokens {{ {r_tag}")
                        }),
                    });
                }
            }
        }
    }

    classify_single(s, end, out, true);
}

fn classify_single(s: &str, end: usize, out: &mut Vec<Candidate>, allow_ranges: bool) {
    let top_weight = cost(1.1);
    let (sign, rest_signed) = parse_sign(s);
    let sign_tag = match sign {
        Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
        None => String::new(),
    };

    let (rest, per_unit_opt) = parse_per_unit(rest_signed);
    let per_unit_tag = match per_unit_opt {
        Some(u) => format!(" morphosyntactic_features: \"{u}\""),
        None => String::new(),
    };

    // Check if currency is prefix or suffix:
    let (currency_maj, num_str, is_prefix) = if let Some((maj, n_str)) = parse_currency_prefix(rest) {
        (maj, n_str, true)
    } else if let Some((n_str, maj)) = parse_currency_suffix(rest) {
        (maj, n_str, false)
    } else {
        return;
    };

    // 1. Money range with single currency: "10-20 $", "৫০-১০০ টাকা"
    if allow_ranges {
        for dash in ["-", "–", "—"] {
            if let Some((n1_s, n2_s)) = num_str.split_once(dash) {
                let n1_s = n1_s.trim();
                let n2_s = n2_s.trim();
                let n1_r = numbers::graph(n1_s);
                let n2_r = numbers::graph(n2_s);
                for (n1_w, n1_c) in &n1_r {
                    for (n2_w, n2_c) in &n2_r {
                        let sign_c = sign_tag.clone();
                        let per_c = per_unit_tag.clone();
                        let n1_w_c = n1_w.clone();
                        let n2_w_c = n2_w.clone();
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(-0.01) + n1_c + n2_c,
                            rank: 7,
                            tag: super::lazy::tag(move || {
                                format!(
                                    "money {{ {sign_c}currency_maj: \"{currency_maj}\" integer_part: \"{n1_w_c} থেকে {n2_w_c}\"{per_c} }}"
                                )
                            }),
                        });
                    }
                }
            }
        }
    }

    // 2. Money with scale/quantity: e.g. "৳২.৫ কোটি", "$50k", "$2.5M", "$5 মিলিয়ন"
    let mut matched_qty = None;
    for &(shorthand, full_word) in MAG_SHORTHAND {
        if let Some(prefix) = num_str.strip_suffix(shorthand) {
            matched_qty = Some((prefix.trim_end_matches(' '), full_word));
            break;
        }
    }
    if matched_qty.is_none() {
        for &qty in QUANTITIES.iter() {
            if let Some(prefix) = num_str.strip_suffix(qty) {
                matched_qty = Some((prefix.trim_end_matches(' '), qty));
                break;
            }
        }
    }

    if let Some((n_part, qty_word)) = matched_qty {
        let n_spoken = if let Some((i_s, f_s)) = n_part.split_once('.') {
            let i_r = numbers::graph(i_s);
            let f_sp = numbers::digitwise(f_s);
            if let (Some((i_val, _)), Some(f_val)) = (i_r.into_iter().next(), f_sp) {
                Some(format!("{i_val} দশমিক {f_val}"))
            } else {
                None
            }
        } else {
            numbers::graph(n_part).into_iter().next().map(|r| r.0)
        };

        if let Some(num_spoken) = n_spoken {
            let sign_c = sign_tag.clone();
            let per_c = per_unit_tag.clone();
            out.push(Candidate {
                end,
                cost: top_weight + cost(-0.01),
                rank: 7,
                tag: super::lazy::tag(move || {
                    format!("money {{ {sign_c}currency_maj: \"{currency_maj}\" integer_part: \"{num_spoken} {qty_word}\"{per_c} }}")
                }),
            });
            return;
        }
    }

    // 3. Decimal and major/minor currencies
    let num_clean = strip_trailing_zero_dot(num_str);
    if let Some((int_s, frac_s)) = num_clean.split_once('.') {
        if !int_s.is_empty() && !frac_s.is_empty() && frac_s.chars().all(numbers::is_digit) {
            let frac_digit_count = frac_s.chars().count();
            let min_name_opt = MAJ_TO_MIN.get(currency_maj).copied();

            if frac_digit_count <= 2 && min_name_opt.is_some() {
                // Major and Minor path:
                let min_name = min_name_opt.unwrap();
                if let Some(frac_words) = two_digit_fraction_to_cardinal(frac_s) {
                    numbers::for_each_graph(int_s, &mut |int_words, int_cost| {
                        out.push(Candidate {
                            end,
                            cost: top_weight + int_cost,
                            rank: 7,
                            tag: super::lazy::tag(|| {
                                format!(
                                    "money {{ {sign_tag}currency_maj: \"{currency_maj}\" integer_part: \"{int_words}\" fractional_part: \"{frac_words}\" currency_min: \"{min_name}\"{per_unit_tag} }}"
                                )
                            }),
                        });
                    });
                }
            } else {
                // Decimal path (3+ decimal digits or currency without minor unit)
                if let Some(frac_words) = numbers::digitwise(frac_s) {
                    numbers::for_each_graph(int_s, &mut |int_words, int_cost| {
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(-0.0005) + int_cost,
                            rank: 7,
                            tag: super::lazy::tag(|| {
                                format!(
                                    "money {{ {sign_tag}currency_maj: \"{currency_maj}\" integer_part: \"{int_words}\" fractional_part: \"{frac_words}\"{per_unit_tag} }}"
                                )
                            }),
                        });
                    });
                }
            }
        }
    } else {
        // Major only path:
        numbers::for_each_graph(num_clean, &mut |int_words, int_cost| {
            let rank_val = if is_prefix { 7 } else { 7 };
            out.push(Candidate {
                end,
                cost: top_weight + int_cost,
                rank: rank_val,
                tag: super::lazy::tag(|| {
                    format!("money {{ {sign_tag}currency_maj: \"{currency_maj}\" integer_part: \"{int_words}\"{per_unit_tag} }}")
                }),
            });
        });
    }
}
