//! Telugu money classification grammar.

use super::lazy;
use super::numbers::{self, is_digit};
use crate::lattice::Candidate;
use crate::text::cost;
use crate::tsv;
use std::collections::HashMap;
use std::sync::LazyLock;

tsv!(CURRENCY, "te/money/currency.tsv");
tsv!(CURRENCY_SINGULAR, "te/money/currency_singular.tsv");
tsv!(MAJOR_MINOR, "te/money/major_minor_currencies.tsv");

type CurrencyIndex = HashMap<char, Vec<&'static crate::text::Row>>;
static CURRENCY_INDEX: LazyLock<(CurrencyIndex, CurrencyIndex)> = LazyLock::new(|| {
    let mut starts: CurrencyIndex = HashMap::new();
    let mut ends: CurrencyIndex = HashMap::new();
    for row in CURRENCY.rows() {
        if let (Some(first), Some(last)) = (row.key.chars().next(), row.key.chars().next_back()) {
            starts.entry(first).or_default().push(row);
            ends.entry(last).or_default().push(row);
        }
    }
    (starts, ends)
});

pub fn possible_start(c: char) -> bool {
    is_digit(c) || matches!(c, '-' | '−' | '~' | '±') || CURRENCY_INDEX.0.contains_key(&c)
}

pub fn has_currency_prefix(s: &str) -> bool {
    s.chars()
        .next()
        .and_then(|c| CURRENCY_INDEX.0.get(&c))
        .is_some_and(|rows| rows.iter().any(|row| s.starts_with(row.key.as_str())))
}

pub fn is_major_name(name: &str) -> bool {
    matches!(name, "అమెరికన్ డాలర్లు" | "అమెరికన్ డాలర్")
        || CURRENCY.rows().iter().chain(CURRENCY_SINGULAR.rows()).any(|row| row.value == name)
        || MAJOR_MINOR.rows().iter().any(|row| row.key == name)
}

const PER_UNITS: [(&str, &str); 26] = [
    ("kWh", "కిలోవాట్ గంటకు"),
    ("min", "నిమిషానికి"),
    ("sec", "సెకనుకు"),
    ("day", "రోజుకు"),
    ("mL", "మిల్లీలీటరుకు"),
    ("ml", "మిల్లీలీటరుకు"),
    ("mg", "మిల్లీగ్రాముకు"),
    ("kg", "కిలోగ్రాముకు"),
    ("km", "కిలోమీటరుకు"),
    ("hr", "గంటకు"),
    ("wk", "వారానికి"),
    ("mo", "నెలకు"),
    ("yr", "సంవత్సరానికి"),
    ("lb", "పౌండుకు"),
    ("oz", "ఔన్సుకు"),
    ("m²", "చదరపు మీటరుకు"),
    ("m2", "చదరపు మీటరుకు"),
    ("ft²", "చదరపు అడుగుకు"),
    ("h", "గంటకు"),
    ("s", "సెకనుకు"),
    ("d", "రోజుకు"),
    ("g", "గ్రాముకు"),
    ("t", "టన్నుకు"),
    ("l", "లీటరుకు"),
    ("L", "లీటరుకు"),
    ("m", "మీటరుకు"),
];

static INPUT_CHARS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    CURRENCY
        .rows()
        .iter()
        .flat_map(|row| row.key.chars())
        .chain(PER_UNITS.iter().flat_map(|(key, _)| key.chars()))
        .collect()
});

pub fn possible_char(c: char) -> bool {
    is_digit(c)
        || matches!(c, '-' | '–' | '.' | ',' | ' ' | '/' | 'k' | 'K' | 'M' | 'B' | 'T' | '~' | '±')
        || INPUT_CHARS.contains(&c)
}

// A money expression must contain a currency key either before its amount or
// immediately after a numeric/range/shorthand amount. Per-unit text follows
// the currency and is deliberately left to the classifier.
pub fn possible_currency(input: &str) -> bool {
    for (pos, c) in input.char_indices() {
        if CURRENCY_INDEX
            .0
            .get(&c)
            .is_some_and(|rows| rows.iter().any(|row| input[pos..].starts_with(row.key.as_str())))
        {
            return true;
        }
        if !is_digit(c) && !matches!(c, '-' | '–' | '.' | ',' | ' ' | 'k' | 'K' | 'M' | 'B' | 'T') {
            return false;
        }
    }
    false
}

fn extract_per_unit(s: &str) -> (&str, Option<&'static str>) {
    for &(sym, spoken) in &PER_UNITS {
        if let Some(rest) = s.strip_suffix(sym) {
            let trimmed = rest.strip_suffix(' ').unwrap_or(rest);
            if let Some(before_slash) = trimmed.strip_suffix('/') {
                return (before_slash.strip_suffix(' ').unwrap_or(before_slash), Some(spoken));
            }
        }
    }
    (s, None)
}

fn parse_shorthand(num_s: &str) -> Option<(String, crate::text::Cost)> {
    let suffix = num_s.chars().last()?;
    let magnitude = match suffix {
        'k' | 'K' => "వేల",
        'M' => "మిలియన్",
        'B' => "బిలియన్",
        'T' => "ట్రిలియన్",
        _ => return None,
    };
    let number = &num_s[..num_s.len() - suffix.len_utf8()];
    let number = number.strip_suffix(' ').unwrap_or(number);
    let (integer, fraction) = number.split_once('.').map_or((number, None), |(a, b)| (a, Some(b)));
    if !(1..=6).contains(&integer.chars().count()) || !integer.chars().all(is_digit) {
        return None;
    }
    let (word, weight) = super::numeric::fraction_cardinal(integer)?;
    if let Some(fraction) = fraction {
        if !(1..=4).contains(&fraction.chars().count()) {
            return None;
        }
        return Some((format!("{word} దశాంశం {} {magnitude}", numbers::digitwise(fraction)?), weight));
    }
    if matches!(suffix, 'k' | 'K') && (integer == "1" || integer == "౧") {
        Some(("వెయ్యి".to_owned(), weight))
    } else {
        Some((format!("{} {magnitude}", numbers::to_attributive(&word)), weight))
    }
}

fn range_amount(s: &str) -> Option<(String, crate::text::Cost)> {
    if let Some(spoken) = parse_shorthand(s) {
        return Some(spoken);
    }
    if let Some((integer, fraction)) = s.split_once('.') {
        let (integer, weight) = super::numeric::fraction_cardinal(integer)?;
        return Some((format!("{integer} దశాంశం {}", numbers::digitwise(fraction)?), weight));
    }
    super::numeric::fraction_cardinal(s)
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (core, per_unit_sp) = extract_per_unit(s);
    let per_unit_field = per_unit_sp.map(|pu| format!(" morphosyntactic_features: \"{pu}\"")).unwrap_or_default();

    let (core_nosign, negative) = if let Some(r) = core.strip_prefix('-') { (r, true) } else { (core, false) };
    let sign_p = if negative { "negative: \"true\" " } else { "" };

    // Check prefix currency
    for row in core_nosign.chars().next().and_then(|c| CURRENCY_INDEX.0.get(&c)).into_iter().flatten() {
        let sym = row.key.as_str();
        if let Some(amt_s) = core_nosign.strip_prefix(sym) {
            let amt_s = amt_s.strip_prefix(' ').unwrap_or(amt_s);
            if amt_s.is_empty() {
                continue;
            }

            let maj_name = if sym == "USD" || sym == "US$" {
                "అమెరికన్ డాలర్లు"
            } else {
                row.value.as_str()
            };
            let maj_sing_name = if sym == "USD" || sym == "US$" {
                "అమెరికన్ డాలర్"
            } else {
                CURRENCY_SINGULAR.exact(sym).next().map(|r| r.value.as_str()).unwrap_or(maj_name)
            };

            // Range endpoints may repeat any symbol denoting the same major currency.
            if !negative {
                for dash in [" - ", " – ", "–", "-"] {
                    if let Some((a1, a2)) = amt_s.split_once(dash) {
                        let mut endings = vec![a2];
                        for other in CURRENCY.rows() {
                            let other_name = if matches!(other.key.as_str(), "USD" | "US$") {
                                "అమెరికన్ డాలర్లు"
                            } else {
                                other.value.as_str()
                            };
                            if other_name == maj_name {
                                if let Some(rest) = a2.strip_prefix(other.key.as_str()) {
                                    endings.push(rest.strip_prefix(' ').unwrap_or(rest));
                                }
                            }
                        }
                        if let Some((s1, w1)) = range_amount(a1) {
                            for ending in endings {
                                if let Some((s2, w2)) = range_amount(ending) {
                                    out.push(Candidate { end, cost: cost(1.08) - cost(0.002) + w1 + w2, rank: 8,
                                        tag: lazy::tag(|| format!("money {{ currency_maj: \"{maj_name}\" integer_part: \"{s1} నుండి {s2}\"{per_unit_field} }}")) });
                                }
                            }
                        }
                    }
                }
            }

            // Shorthand: $50k, $2.5M
            if let Some((sh_spoken, weight)) = parse_shorthand(amt_s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) - cost(0.002) + weight,
                    rank: 8,
                    tag: lazy::tag(|| {
                        format!(
                            "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"{sh_spoken}\"{per_unit_field} }}"
                        )
                    }),
                });
            }

            // Decimal amount
            if let Some((int_s, frac_s)) = amt_s.split_once('.') {
                let frac_trimmed = frac_s.trim_end_matches(['0', '\u{0c66}']);

                // 3+ decimal digits -> guarded decimal digits
                if frac_s.chars().skip(2).any(|c| is_digit(c) && !numbers::is_zero(c)) && frac_s.chars().all(is_digit) {
                    if let (Some((int_sp, inner_cost)), Some(frac_sp)) =
                        (super::numeric::fraction_cardinal(int_s), numbers::digitwise(frac_s))
                    {
                        out.push(Candidate {
                            end,
                            cost: cost(1.08) - cost(0.0005) + inner_cost,
                            rank: 8,
                            tag: lazy::tag(|| {
                                format!(
                                    "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"{int_sp}\" fractional_part: \"{frac_sp}\"{per_unit_field} }}"
                                )
                            }),
                        });
                    }
                }

                // If trailing zeros only (e.g. 50.00)
                if !frac_s.is_empty() && frac_trimmed.is_empty() {
                    if int_s == "1" || int_s == "\u{0c67}" {
                        out.push(Candidate {
                            end,
                            cost: cost(1.08) - cost(0.001),
                            rank: 8,
                            tag: lazy::tag(|| {
                                format!(
                                    "money {{ {sign_p}currency_maj: \"{maj_sing_name}\" integer_part: \"ఒక\"{per_unit_field} }}"
                                )
                            }),
                        });
                    } else if let Some((int_sp, inner_cost)) = super::numeric::fraction_cardinal(int_s) {
                        let attr = numbers::to_attributive(&int_sp);
                        out.push(Candidate {
                            end,
                            cost: cost(1.08) + inner_cost,
                            rank: 8,
                            tag: lazy::tag(|| {
                                format!(
                                    "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"{attr}\"{per_unit_field} }}"
                                )
                            }),
                        });
                    }
                }

                // 2-digit decimals with minor currency
                if let Some(min_row) = MAJOR_MINOR.exact(maj_name).next().filter(|_| frac_s.chars().all(is_digit)) {
                    let min_name = min_row.value.as_str();

                    // Canonicalize fractional digits: e.g. "5" -> 50, "05" -> 5, "50" -> 50
                    let digits: Vec<_> = frac_trimmed.chars().filter_map(numbers::digit_val).collect();
                    let frac_val = match digits.as_slice() {
                        [0, n @ 1..=9] => Some(*n),
                        [n @ 1..=9] => Some(*n * 10),
                        [a @ 1..=9, b] => Some(*a * 10 + *b),
                        _ => None,
                    };

                    if let Some(fv) = frac_val {
                        if fv > 0 && fv < 100 {
                            let frac_sp = numbers::spell_two_digits(fv).unwrap_or("");
                            if int_s == "0" || int_s == "\u{0c66}" {
                                out.push(Candidate {
                                    end,
                                    cost: cost(1.08),
                                    rank: 8,
                                    tag: lazy::tag(|| {
                                        format!(
                                            "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"సున్నా\" fractional_part: \"{frac_sp}\" currency_min: \"{min_name}\"{per_unit_field} }}"
                                        )
                                    }),
                                });
                            } else if int_s == "1" || int_s == "\u{0c67}" {
                                out.push(Candidate {
                                    end,
                                    cost: cost(1.08) - cost(0.001),
                                    rank: 8,
                                    tag: lazy::tag(|| {
                                        format!(
                                            "money {{ {sign_p}currency_maj: \"{maj_sing_name}\" integer_part: \"ఒక\" fractional_part: \"{frac_sp}\" currency_min: \"{min_name}\"{per_unit_field} }}"
                                        )
                                    }),
                                });
                            } else if let Some((int_sp, inner_cost)) = super::numeric::fraction_cardinal(int_s) {
                                let attr = numbers::to_attributive(&int_sp);
                                out.push(Candidate {
                                    end,
                                    cost: cost(1.08) + inner_cost,
                                    rank: 8,
                                    tag: lazy::tag(|| {
                                        format!(
                                            "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"{attr}\" fractional_part: \"{frac_sp}\" currency_min: \"{min_name}\"{per_unit_field} }}"
                                        )
                                    }),
                                });
                            }
                        }
                    }
                }
            }

            if let Some(recipe) = super::numeric::cardinal_recipe(amt_s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) + recipe.weight,
                    rank: 8,
                    tag: lazy::tag(|| {
                        let attr = numbers::to_attributive(&recipe.render());
                        format!(
                            "money {{ {sign_p}currency_maj: \"{maj_name}\" integer_part: \"{attr}\"{per_unit_field} }}"
                        )
                    }),
                });
                if (amt_s == "1" || amt_s == "౧")
                    && (matches!(sym, "USD" | "US$") || CURRENCY_SINGULAR.contains_key(sym))
                {
                    out.push(Candidate { end, cost: cost(1.08) - cost(0.001), rank: 8,
                        tag: lazy::tag(|| format!("money {{ {sign_p}currency_maj: \"{maj_sing_name}\" integer_part: \"ఒక\"{per_unit_field} }}")) });
                }
            }
        }
    }

    // Check suffix currency: e.g. "500 USD", "10-20 USD"
    for row in core_nosign.chars().next_back().and_then(|c| CURRENCY_INDEX.1.get(&c)).into_iter().flatten() {
        let sym = row.key.as_str();
        if let Some(amt_s) = core_nosign.strip_suffix(sym) {
            let amt_s = amt_s.strip_suffix(' ').unwrap_or(amt_s);
            if amt_s.is_empty() {
                continue;
            }

            let maj_name = row.value.as_str();
            let maj_sing_name = CURRENCY_SINGULAR.exact(sym).next().map(|r| r.value.as_str()).unwrap_or(maj_name);

            if !negative {
                for dash in [" - ", " – ", "–", "-"] {
                    if let Some((a1, a2)) = amt_s.split_once(dash) {
                        if let (Some((s1, w1)), Some((s2, w2))) = (range_amount(a1), range_amount(a2)) {
                            out.push(Candidate { end, cost: cost(1.08) - cost(0.002) + w1 + w2, rank: 8,
                                tag: lazy::tag(|| format!("money {{ integer_part: \"{s1} నుండి {s2}\" currency_maj: \"{maj_name}\"{per_unit_field} }}")) });
                        }
                    }
                }
            }

            // Suffix Shorthand: 50k USD
            if let Some((sh_spoken, weight)) = parse_shorthand(amt_s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) - cost(0.002) + weight,
                    rank: 8,
                    tag: lazy::tag(|| {
                        format!(
                            "money {{ {sign_p}integer_part: \"{sh_spoken}\" currency_maj: \"{maj_name}\"{per_unit_field} }}"
                        )
                    }),
                });
            }

            if let Some((integer, fraction)) = amt_s.split_once('.') {
                if !fraction.is_empty() && fraction.chars().all(is_digit) {
                    if let Some((spoken, inner_cost)) = super::numeric::fraction_cardinal(integer) {
                        if fraction.chars().skip(2).any(|c| !numbers::is_zero(c)) {
                            if let Some(frac) = numbers::digitwise(fraction) {
                                out.push(Candidate { end, cost: cost(1.08) - cost(0.0005) + inner_cost, rank: 8,
                                    tag: lazy::tag(|| format!("money {{ {sign_p}integer_part: \"{spoken}\" fractional_part: \"{frac}\" currency_maj: \"{maj_name}\"{per_unit_field} }}")) });
                            }
                        }
                        let trimmed = fraction.trim_end_matches(['0', '౦']);
                        if trimmed.is_empty() {
                            let one = integer == "1" || integer == "౧";
                            let name = if one { maj_sing_name } else { maj_name };
                            let attr = numbers::to_attributive(&spoken);
                            out.push(Candidate { end, cost: cost(1.08) + inner_cost - if one { cost(0.001) } else { 0 }, rank: 8,
                                tag: lazy::tag(|| format!("money {{ {sign_p}integer_part: \"{attr}\" currency_maj: \"{name}\"{per_unit_field} }}")) });
                        } else {
                            let digits: Vec<_> = trimmed.chars().filter_map(numbers::digit_val).collect();
                            let value = match digits.as_slice() {
                                [0, n @ 1..=9] => Some(*n),
                                [n @ 1..=9] => Some(*n * 10),
                                [a @ 1..=9, b] => Some(*a * 10 + *b),
                                _ => None,
                            };
                            if let Some(frac) = value.and_then(numbers::spell_two_digits) {
                                let one = integer == "1" || integer == "౧";
                                let name = if one { maj_sing_name } else { maj_name };
                                if let Some(minor) = MAJOR_MINOR.exact(name).next() {
                                    let attr = numbers::to_attributive(&spoken);
                                    out.push(Candidate { end, cost: cost(1.08) + inner_cost - if one { cost(0.001) } else { 0 }, rank: 8,
                                        tag: lazy::tag(|| format!("money {{ {sign_p}integer_part: \"{attr}\" fractional_part: \"{frac}\" currency_maj: \"{name}\" currency_min: \"{}\"{per_unit_field} }}", minor.value)) });
                                }
                            }
                        }
                    }
                }
            }

            if let Some(recipe) = super::numeric::cardinal_recipe(amt_s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) + recipe.weight,
                    rank: 8,
                    tag: lazy::tag(|| {
                        let attr = numbers::to_attributive(&recipe.render());
                        format!(
                            "money {{ {sign_p}integer_part: \"{attr}\" currency_maj: \"{maj_name}\"{per_unit_field} }}"
                        )
                    }),
                });
                if (amt_s == "1" || amt_s == "౧") && CURRENCY_SINGULAR.contains_key(sym) {
                    out.push(Candidate { end, cost: cost(1.08) - cost(0.001), rank: 8,
                        tag: lazy::tag(|| format!("money {{ {sign_p}integer_part: \"ఒక\" currency_maj: \"{maj_sing_name}\"{per_unit_field} }}")) });
                }
            }
        }
    }
}
