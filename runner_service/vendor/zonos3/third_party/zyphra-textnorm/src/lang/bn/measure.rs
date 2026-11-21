//! Bengali Measure classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const UNITS_DATA: &str = include_str!("../../../data/bn/measure/unit.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static UNITS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let mut v: Vec<(&'static str, &'static str)> = rows(UNITS_DATA).collect();
    // Sort by key length descending so longer unit matches take precedence
    v.sort_by(|a, b| b.0.len().cmp(&a.0.len()));
    v
});

static UNITS_BY_LAST: LazyLock<std::collections::HashMap<char, Vec<(&'static str, &'static str)>>> =
    LazyLock::new(|| {
        let mut index = std::collections::HashMap::<char, Vec<(&'static str, &'static str)>>::new();
        for &(key, spoken) in UNITS.iter() {
            if let Some(last) = key.chars().next_back() {
                index.entry(last).or_default().push((key, spoken));
            }
        }
        index
    });

static UNITS_BY_FIRST: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = std::collections::HashMap::<char, Vec<&'static str>>::new();
    for &(key, _) in UNITS.iter() {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

static LEXICAL_BOUND_SAFE: LazyLock<bool> =
    LazyLock::new(|| UNITS.iter().all(|(key, _)| key.chars().next().is_some_and(|c| !number_char(c))));

pub fn lexical_bound_safe() -> bool {
    *LEXICAL_BOUND_SAFE
}

pub fn last_unit_end(input: &str, start: usize) -> Option<usize> {
    let tail = &input[start..];
    let first = tail.chars().next()?;
    UNITS_BY_FIRST.get(&first)?.iter().filter(|key| tail.starts_with(**key)).map(|key| start + key.len()).max()
}

pub fn possible_end(s: &str, c: char, has_dimension: bool) -> bool {
    (has_dimension && (numbers::is_digit(c) || (c.is_whitespace() && s.trim_end().ends_with(numbers::is_digit))))
        || UNITS_BY_LAST.get(&c).is_some_and(|units| units.iter().any(|(key, _)| s.ends_with(key)))
}

pub fn number_char(c: char) -> bool {
    numbers::is_digit(c) || matches!(c, '-' | '−' | '~' | '±' | '.' | ',' | 'x' | 'X' | '*' | ' ' | '–' | '—')
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

fn is_glued_decade_s(s: &str) -> bool {
    if !s.ends_with('s') {
        return false;
    }
    let stem = &s[..s.len() - 1];
    let stem = stem.strip_prefix('\'').unwrap_or(stem);
    if stem.len() == 4
        && (stem.starts_with('1') || stem.starts_with('2') || stem.starts_with('১') || stem.starts_with('২'))
        && (stem.ends_with('0') || stem.ends_with('০'))
        && stem.chars().all(numbers::is_digit)
    {
        return true;
    }
    if stem.len() == 2 && (stem.ends_with('0') || stem.ends_with('০')) && stem.chars().all(numbers::is_digit) {
        return true;
    }
    false
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    // 1. Handling cardinal clubbed with symbol: ১০x২০ -> দশ বাই কুড়ি
    for sym in ['x', 'X', '*'] {
        if let Some((left, right)) = s.split_once(sym) {
            let (l_sign, l_num) = parse_sign(left.trim());
            let (r_sign, r_num) = parse_sign(right.trim());
            let l_sign_tag = match l_sign {
                Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
                None => String::new(),
            };
            let r_sign_tag = match r_sign {
                Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
                None => String::new(),
            };

            let left_r = numbers::graph(l_num);
            let right_r = numbers::graph(r_num);
            for (l_w, l_c) in &left_r {
                for (r_w, r_c) in &right_r {
                    let l_sign_c = l_sign_tag.clone();
                    let r_sign_c = r_sign_tag.clone();
                    let l_w_c = l_w.clone();
                    let r_w_c = r_w.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(0.1) + l_c + r_c,
                        rank: 6,
                        tag: super::lazy::tag(move || {
                            format!(
                                "measure {{ cardinal {{ {l_sign_c}integer: \"{l_w_c}\" }} units: \"বাই\" }} }} tokens {{ cardinal {{ {r_sign_c}integer: \"{r_w_c}\" }}"
                            )
                        }),
                    });
                }
            }
        }
    }

    // 2. Measure with unit suffix
    let units = s.chars().next_back().and_then(|c| UNITS_BY_LAST.get(&c));
    for &(unit_key, unit_spoken) in units.into_iter().flatten() {
        if let Some(prefix) = s.strip_suffix(unit_key) {
            let num_str = prefix.trim_end_matches(' ');
            if num_str.is_empty() {
                continue;
            }

            // Exclude glued decades like "2020s" matching unit "s"
            if unit_key == "s" && is_glued_decade_s(s) {
                continue;
            }

            let (sign, rest_num) = parse_sign(num_str);
            let sign_tag = match sign {
                Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
                None => String::new(),
            };

            // 2a. Range measure: num1 + ("-" | "–" | "—") + num2 + unit
            let mut is_range = false;
            for dash in ["-", "–", "—"] {
                if let Some((n1_s, n2_s)) = rest_num.split_once(dash) {
                    let n1_s = n1_s.trim();
                    let n2_s = n2_s.trim();
                    let n1_spoken = parse_range_num(n1_s);
                    let n2_spoken = parse_range_num(n2_s);
                    if let (Some(n1_w), Some(n2_w)) = (n1_spoken, n2_spoken) {
                        is_range = true;
                        let sign_c = sign_tag.clone();
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(-0.05),
                            rank: 6,
                            tag: super::lazy::tag(move || {
                                format!(
                                    "measure {{ cardinal {{ {sign_c}integer: \"{n1_w} থেকে {n2_w}\" }} units: \"{unit_spoken}\"  }}"
                                )
                            }),
                        });
                    }
                }
            }

            if is_range {
                continue;
            }

            // 2b. Decimal measure: int.frac + unit
            if let Some((int_s, frac_s)) = rest_num.split_once('.') {
                if !int_s.is_empty() && !frac_s.is_empty() && frac_s.chars().all(numbers::is_digit) {
                    if let Some(frac_val) = numbers::digitwise(frac_s) {
                        numbers::for_each_graph(int_s, &mut |int_val, int_cost| {
                            out.push(Candidate {
                                end,
                                cost: top_weight + cost(-0.05) + int_cost,
                                rank: 6,
                                tag: super::lazy::tag(|| {
                                    format!(
                                        "measure {{ decimal {{ {sign_tag}integer_part: \"{int_val}\" fractional_part: \"{frac_val}\" }} units: \"{unit_spoken}\"  }}"
                                    )
                                }),
                            });
                        });
                    }
                }
            }

            // 2c. Cardinal measure: int + unit
            numbers::for_each_graph(rest_num, &mut |card_val, card_cost| {
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.05) + card_cost,
                    rank: 6,
                    tag: super::lazy::tag(|| {
                        format!(
                            "measure {{ cardinal {{ {sign_tag}integer: \"{card_val}\" }} units: \"{unit_spoken}\"  }}"
                        )
                    }),
                });
            });
        }
    }
}

fn parse_range_num(s: &str) -> Option<String> {
    if let Some((int_s, frac_s)) = s.split_once('.') {
        let int_r = numbers::graph(int_s);
        let int_val = int_r.first()?.0.as_str();
        let frac_val = numbers::digitwise(frac_s)?;
        Some(format!("{int_val} দশমিক {frac_val}"))
    } else {
        numbers::graph(s).into_iter().next().map(|r| r.0)
    }
}
