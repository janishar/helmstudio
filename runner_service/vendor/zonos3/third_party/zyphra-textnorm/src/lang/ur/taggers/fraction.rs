//! Productive Urdu fraction grammar.

use super::Sink;
use super::cardinal::enumerate_cardinal;
use crate::text::cost;
use std::collections::HashSet;
use std::sync::LazyLock;

static FRACTION_BOUNDARIES: LazyLock<(HashSet<char>, HashSet<char>)> = LazyLock::new(|| {
    let mut first = HashSet::new();
    let mut last = HashSet::new();
    for (key, _) in COMMON_FRACTIONS.iter().copied() {
        if let Some(c) = key.chars().next() {
            first.insert(c);
        }
        if let Some(c) = key.chars().last() {
            last.insert(c);
        }
    }
    for &(c, _, _) in VULGAR_OTHER {
        last.insert(c);
    }
    (first, last)
});

pub(crate) fn possible_span(first: char, last: char, slash: bool) -> bool {
    (first.is_ascii_digit() || FRACTION_BOUNDARIES.0.contains(&first))
        && ((last.is_ascii_digit() && slash) || (!last.is_ascii_digit() && FRACTION_BOUNDARIES.1.contains(&last)))
}

pub(crate) fn possible_start(c: char) -> bool {
    c.is_ascii_digit() || FRACTION_BOUNDARIES.0.contains(&c)
}

pub(crate) fn continuation(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '-' | ',' | '/' | ' ') || FRACTION_BOUNDARIES.1.contains(&c)
}

const COMMON_FRACTIONS_DATA: &str = include_str!("../../../../data/ur/fraction/common_fractions.tsv");
const PAUNE_DATA: &str = include_str!("../../../../data/ur/whitelist/paune_mappings.tsv");

static COMMON_FRACTIONS: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| COMMON_FRACTIONS_DATA.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t')).collect());

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

const VULGAR_OTHER: &[(char, &str, &str)] = &[
    ('⅓', "ایک", "تین"),
    ('⅔', "دو", "تین"),
    ('⅕', "ایک", "پانچ"),
    ('⅖', "دو", "پانچ"),
    ('⅗', "تین", "پانچ"),
    ('⅘', "چار", "پانچ"),
    ('⅙', "ایک", "چھ"),
    ('⅚', "پانچ", "چھ"),
    ('⅛', "ایک", "آٹھ"),
    ('⅜', "تین", "آٹھ"),
    ('⅝', "پانچ", "آٹھ"),
    ('⅞', "سات", "آٹھ"),
    ('⅐', "ایک", "سات"),
    ('⅑', "ایک", "نو"),
    ('⅒', "ایک", "دس"),
];

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() {
        return;
    }
    let (neg, s_start) = if text[start] == '-' { (true, start + 1) } else { (false, start) };
    if s_start >= text.len() {
        return;
    }

    let remaining_text = &text[s_start..];

    // 1. Common fractions from TSV (weight -0.3)
    for &(frac_key, spoken) in COMMON_FRACTIONS.iter() {
        let key_len = frac_key.chars().count();
        if remaining_text.len() >= key_len && remaining_text[..key_len].iter().copied().eq(frac_key.chars()) {
            let end = s_start + key_len;
            let c = cost(1.05) + cost(-0.3);
            sink.push(end, c, 0, move || {
                format!(
                    "fraction {{ {}morphosyntactic_features: \"{}\" }}",
                    if neg { "negative: \"true\" " } else { "" },
                    spoken
                )
            });
        }
    }

    // 2. Dedh / Dhai (1.5 / 2.5) forms (weight -0.2)
    // 1 1/2, 1½ -> ڈیڑھ
    // 2 1/2, 2½ -> ڈھائی
    if remaining_text.starts_with(&['1', ' ', '1', '/', '2']) {
        let end = s_start + 5;
        let c = cost(1.05) + cost(-0.2);
        sink.push(end, c, 0, move || {
            format!("fraction {{ {}morphosyntactic_features: \"ڈیڑھ\" }}", if neg { "negative: \"true\" " } else { "" })
        });
    } else if remaining_text.starts_with(&['1', '½']) || remaining_text.starts_with(&['1', ' ', '½']) {
        let end = s_start + if remaining_text[1] == ' ' { 3 } else { 2 };
        let c = cost(1.05) + cost(-0.2);
        sink.push(end, c, 0, move || {
            format!("fraction {{ {}morphosyntactic_features: \"ڈیڑھ\" }}", if neg { "negative: \"true\" " } else { "" })
        });
    }
    if remaining_text.starts_with(&['2', ' ', '1', '/', '2']) {
        let end = s_start + 5;
        let c = cost(1.05) + cost(-0.2);
        sink.push(end, c, 0, move || {
            format!(
                "fraction {{ {}morphosyntactic_features: \"ڈھائی\" }}",
                if neg { "negative: \"true\" " } else { "" }
            )
        });
    } else if remaining_text.starts_with(&['2', '½']) || remaining_text.starts_with(&['2', ' ', '½']) {
        let end = s_start + if remaining_text[1] == ' ' { 3 } else { 2 };
        let c = cost(1.05) + cost(-0.2);
        sink.push(end, c, 0, move || {
            format!(
                "fraction {{ {}morphosyntactic_features: \"ڈھائی\" }}",
                if neg { "negative: \"true\" " } else { "" }
            )
        });
    }

    // 3. Paune (X 3/4 or X¾) (weight -0.2)
    // Check for 1..2 digits at start
    let mut num_end = 0;
    while num_end < remaining_text.len() && (remaining_text[num_end].is_ascii_digit() || remaining_text[num_end] == ',')
    {
        num_end += 1;
    }
    if num_end > 0 && num_end <= 2 && (num_end == 1 || remaining_text[0] != '0') {
        let n = remaining_text[..num_end].iter().try_fold(0usize, |n, c| c.to_digit(10).map(|d| n * 10 + d as usize));
        if let Some(n) = n {
            if n < 100 {
                if let Some(paune_word) = PAUNE_MAP[n] {
                    let after = &remaining_text[num_end..];
                    if after.starts_with(&[' ', '3', '/', '4']) {
                        let end = s_start + num_end + 4;
                        let c = cost(1.05) + cost(-0.2);
                        sink.push(end, c, 0, move || {
                            format!(
                                "fraction {{ {}morphosyntactic_features: \"پونے {}\" }}",
                                if neg { "negative: \"true\" " } else { "" },
                                paune_word
                            )
                        });
                    } else if after.starts_with(&['¾']) || after.starts_with(&[' ', '¾']) {
                        let end = s_start + num_end + if after.starts_with(&[' ']) { 2 } else { 1 };
                        let c = cost(1.05) + cost(-0.2);
                        sink.push(end, c, 0, move || {
                            format!(
                                "fraction {{ {}morphosyntactic_features: \"پونے {}\" }}",
                                if neg { "negative: \"true\" " } else { "" },
                                paune_word
                            )
                        });
                    }
                }
            }
        }
    }

    // 4. Savva (X 1/4 or X¼) (weight -0.1) & Sadhe (X 1/2 or X½) (weight -0.1)
    if num_end > 0 {
        let int_chars = &remaining_text[..num_end];
        let after = &remaining_text[num_end..];
        // Savva: 1/4 or ¼
        if after.starts_with(&[' ', '1', '/', '4']) {
            let end = s_start + num_end + 4;
            enumerate_cardinal(int_chars, &mut |int_spoken, internal_cost| {
                let c = cost(1.05) + cost(-0.1) + internal_cost;
                sink.push(end, c, 0, move || {
                    format!(
                        "fraction {{ {}morphosyntactic_features: \"سوا {}\" }}",
                        if neg { "negative: \"true\" " } else { "" },
                        int_spoken
                    )
                });
            });
        } else if after.starts_with(&['¼']) || after.starts_with(&[' ', '¼']) {
            let end = s_start + num_end + if after.starts_with(&[' ']) { 2 } else { 1 };
            enumerate_cardinal(int_chars, &mut |int_spoken, internal_cost| {
                let c = cost(1.05) + cost(-0.1) + internal_cost;
                sink.push(end, c, 0, move || {
                    format!(
                        "fraction {{ {}morphosyntactic_features: \"سوا {}\" }}",
                        if neg { "negative: \"true\" " } else { "" },
                        int_spoken
                    )
                });
            });
        }
        // Sadhe: 1/2 or ½ (except when num == 1 or 2, handled by dedh/dhai)
        let is_1_or_2 = num_end == 1 && (int_chars[0] == '1' || int_chars[0] == '2');
        if !is_1_or_2 {
            if after.starts_with(&[' ', '1', '/', '2']) {
                let end = s_start + num_end + 4;
                enumerate_cardinal(int_chars, &mut |int_spoken, internal_cost| {
                    let c = cost(1.05) + cost(-0.1) + internal_cost;
                    sink.push(end, c, 0, move || {
                        format!(
                            "fraction {{ {}morphosyntactic_features: \"ساڑھے {}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_spoken
                        )
                    });
                });
            } else if after.starts_with(&['½']) || after.starts_with(&[' ', '½']) {
                let end = s_start + num_end + if after.starts_with(&[' ']) { 2 } else { 1 };
                enumerate_cardinal(int_chars, &mut |int_spoken, internal_cost| {
                    let c = cost(1.05) + cost(-0.1) + internal_cost;
                    sink.push(end, c, 0, move || {
                        format!(
                            "fraction {{ {}morphosyntactic_features: \"ساڑھے {}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_spoken
                        )
                    });
                });
            }
        }

        // 5. Mixed vulgar: <cardinal> [ ] vulgar_other
        let mut v_idx = 0;
        if after.starts_with(&[' ']) {
            v_idx = 1;
        }
        if v_idx < after.len() {
            let ch = after[v_idx];
            if let Some(&(_, num, den)) = VULGAR_OTHER.iter().find(|(vc, _, _)| *vc == ch) {
                let end = s_start + num_end + v_idx + 1;
                enumerate_cardinal(int_chars, &mut |int_spoken, internal_cost| {
                    let c = cost(1.05) + internal_cost;
                    sink.push(end, c, 0, move || {
                        format!(
                            "fraction {{ {}integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_spoken,
                            num,
                            den
                        )
                    });
                });
            }
        }
    }

    // 6. Regular fraction: [integer_part " "] numerator / denominator
    // Can have optional integer part before space
    // Let's find any '/' in remaining_text
    for i in 0..remaining_text.len() {
        if remaining_text[i] == '/' {
            // Find numerator start and end
            // Optional space before '/': " / " vs "/"
            let num_r_end = if i > 0 && remaining_text[i - 1] == ' ' { i - 1 } else { i };
            let mut num_r_start = num_r_end;
            while num_r_start > 0
                && (remaining_text[num_r_start - 1].is_ascii_digit() || remaining_text[num_r_start - 1] == ',')
            {
                num_r_start -= 1;
            }
            if num_r_start == num_r_end {
                continue;
            }

            // Find denominator start and end
            let den_r_start = if i + 1 < remaining_text.len() && remaining_text[i + 1] == ' ' { i + 2 } else { i + 1 };
            if (num_r_end != i) != (den_r_start != i + 1) {
                continue;
            }
            let mut den_r_end = den_r_start;
            while den_r_end < remaining_text.len()
                && (remaining_text[den_r_end].is_ascii_digit() || remaining_text[den_r_end] == ',')
            {
                den_r_end += 1;
            }
            if den_r_end == den_r_start {
                continue;
            }

            let num_chars = &remaining_text[num_r_start..num_r_end];
            let den_chars = &remaining_text[den_r_start..den_r_end];

            // Case A: With integer part before numerator: "<int> <num>/<den>"
            if num_r_start > 1 && remaining_text[num_r_start - 1] == ' ' {
                let int_r_end = num_r_start - 1;
                let mut int_r_start = int_r_end;
                while int_r_start > 0
                    && (remaining_text[int_r_start - 1].is_ascii_digit() || remaining_text[int_r_start - 1] == ',')
                {
                    int_r_start -= 1;
                }
                if int_r_start == 0 && int_r_end > 0 {
                    let int_chars = &remaining_text[..int_r_end];
                    let end = s_start + den_r_end;
                    enumerate_cardinal(int_chars, &mut |int_spoken, int_cost| {
                        enumerate_cardinal(num_chars, &mut |num_spoken, num_cost| {
                            enumerate_cardinal(den_chars, &mut |den_spoken, den_cost| {
                                let c = cost(1.05) + int_cost + num_cost + den_cost;
                                let int_s = int_spoken.clone();
                                let num_s = num_spoken.clone();
                                let den_s = den_spoken.clone();
                                sink.push(end, c, 0, move || {
                                    format!(
                                        "fraction {{ {}integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\" }}",
                                        if neg { "negative: \"true\" " } else { "" },
                                        int_s,
                                        num_s,
                                        den_s
                                    )
                                });
                            });
                        });
                    });
                }
            }

            // Case B: Without integer part: "<num>/<den>"
            if num_r_start == 0 {
                let end = s_start + den_r_end;
                enumerate_cardinal(num_chars, &mut |num_spoken, num_cost| {
                    enumerate_cardinal(den_chars, &mut |den_spoken, den_cost| {
                        let c = cost(1.05) + num_cost + den_cost;
                        let num_s = num_spoken.clone();
                        let den_s = den_spoken.clone();
                        sink.push(end, c, 0, move || {
                            format!(
                                "fraction {{ {}numerator: \"{}\" denominator: \"{}\" }}",
                                if neg { "negative: \"true\" " } else { "" },
                                num_s,
                                den_s
                            )
                        });
                    });
                });
            }
        }
    }
}
