//! Bengali Cardinal, Decimal, Fraction, and Ordinal classifiers.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const CLASSIFIERS_DATA: &str = include_str!("../../../data/bn/numbers/classifiers.tsv");
const QUANTITIES_DATA: &str = include_str!("../../../data/bn/numbers/quantities.tsv");
const COMMON_FRACTIONS_DATA: &str = include_str!("../../../data/bn/fraction/common_fractions.tsv");
const NEXT_NUMBER_DATA: &str = include_str!("../../../data/bn/fraction/next_number.tsv");
const ORDINAL_EXCEPTIONS_DATA: &str = include_str!("../../../data/bn/ordinal/exceptions.tsv");
const DAY_ORDINAL_DATA: &str = include_str!("../../../data/bn/date/day_ordinal.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static CLASSIFIERS: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| CLASSIFIERS_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static QUANTITIES: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| QUANTITIES_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static COMMON_FRACTIONS: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| rows(COMMON_FRACTIONS_DATA).collect());

static NEXT_NUMBER: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(NEXT_NUMBER_DATA).collect());

static ORDINAL_EXCEPTIONS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ORDINAL_EXCEPTIONS_DATA).collect());

static DAY_ORDINAL: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(DAY_ORDINAL_DATA).collect());

static EXACT_LEXICAL_BY_FIRST: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = std::collections::HashMap::<char, Vec<&'static str>>::new();
    for key in COMMON_FRACTIONS
        .iter()
        .map(|(key, _)| *key)
        .chain(ORDINAL_EXCEPTIONS.keys().copied())
        .chain(DAY_ORDINAL.keys().copied())
    {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

pub fn structural_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(
            c,
            '-' | '−'
                | '~'
                | '±'
                | '.'
                | ','
                | '/'
                | '+'
                | '='
                | '×'
                | '÷'
                | ' '
                | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅐'
                | '⅑'
                | '⅒'
        )
}

pub fn lexical_ending(s: &str) -> bool {
    CLASSIFIERS.iter().any(|&c| s.ends_with(c))
        || QUANTITIES.iter().any(|&q| s.ends_with(q))
        || ORDINAL_EXCEPTIONS.keys().any(|&k| s == k)
        || DAY_ORDINAL.keys().any(|&k| s == k)
        || s.ends_with("তম")
        || s.ends_with("st")
        || s.ends_with("nd")
        || s.ends_with("rd")
        || s.ends_with("th")
        || COMMON_FRACTIONS.iter().any(|(k, _)| s == *k)
}

pub fn last_lexical_end(input: &str, tail_start: usize) -> Option<usize> {
    let tail = &input[tail_start..];
    let mut max_end = None;
    for &c in CLASSIFIERS.iter() {
        if tail.starts_with(c) {
            max_end = Some(max_end.unwrap_or(0).max(tail_start + c.len()));
        }
    }
    for &q in QUANTITIES.iter() {
        if tail.starts_with(q) {
            max_end = Some(max_end.unwrap_or(0).max(tail_start + q.len()));
        }
    }
    for &key in input.chars().next().and_then(|c| EXACT_LEXICAL_BY_FIRST.get(&c)).into_iter().flatten() {
        if input.starts_with(key) {
            max_end = Some(max_end.unwrap_or(0).max(key.len()));
        }
    }
    if tail.starts_with("তম") {
        max_end = Some(max_end.unwrap_or(0).max(tail_start + "তম".len()));
    }
    for &sfx in &["st", "nd", "rd", "th"] {
        if tail.starts_with(sfx) {
            max_end = Some(max_end.unwrap_or(0).max(tail_start + sfx.len()));
        }
    }
    max_end
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some((sign, rest)) = plain_cardinal_parts(s) {
        numbers::for_each_graph(rest, &mut |val, w| {
            out.push(Candidate {
                end,
                cost: cost(1.1) + w,
                rank: 1,
                tag: super::lazy::tag(|| {
                    let sign_tag = sign.map_or_else(String::new, |v| format!("negative: \"{v}\" "));
                    format!("cardinal {{ {sign_tag}integer: \"{val}\" }}")
                }),
            });
        });
        return;
    }
    classify_cardinal(s, end, out);
    classify_decimal(s, end, out);
    classify_fraction(s, end, out);
    classify_ordinal(s, end, out);
}

fn plain_cardinal_parts(s: &str) -> Option<(Option<&'static str>, &str)> {
    let (sign, rest) = parse_sign(s);
    (!rest.is_empty() && rest.chars().all(|c| numbers::is_digit(c) || c == ',')).then_some((sign, rest))
}

pub fn materialize_short_cardinal(chars: &[char], selected_cost: crate::text::Cost) -> Option<String> {
    if let [first, second] = chars {
        if numbers::zero(*first) {
            let first = numbers::digit(*first)?;
            let second = numbers::digit(*second)?;
            return (selected_cost == cost(1.1) + cost(0.5))
                .then(|| format!("cardinal {{ integer: \"{first} {second}\" }}"));
        }
    }
    let (word, weight) = numbers::small_word(chars)?;
    (selected_cost == cost(1.1) + weight).then(|| format!("cardinal {{ integer: \"{word}\" }}"))
}

pub fn materialize_cardinal_chars(chars: &[char], selected_cost: crate::text::Cost, tag: &mut String) -> bool {
    let (sign, mut digits) = match chars.split_first() {
        Some(('-' | '−', rest)) => (Some("true"), rest),
        Some(('~', rest)) => (Some("approx"), rest),
        Some(('±', rest)) => (Some("pm"), rest),
        _ => (None, chars),
    };
    if matches!(sign, Some("approx" | "pm")) {
        while let Some((' ', rest)) = digits.split_first() {
            digits = rest;
        }
    }
    if digits.is_empty() || digits.len() > 14 || !digits.iter().all(|&c| numbers::is_digit(c)) {
        return false;
    }
    tag.reserve(48 + digits.len() * 12);
    tag.push_str("cardinal { ");
    if let Some(sign) = sign {
        tag.push_str("negative: \"");
        tag.push_str(sign);
        tag.push_str("\" ");
    }
    tag.push_str("integer: \"");
    if numbers::append_selected(digits, selected_cost - cost(1.1), tag) {
        tag.push_str("\" }");
        true
    } else {
        tag.clear();
        false
    }
}
pub fn materialize_plain_cardinal(s: &str, selected_cost: crate::text::Cost) -> Option<String> {
    let (sign, rest) = plain_cardinal_parts(s)?;
    let mut tag = None;
    numbers::for_each_graph(rest, &mut |value, weight| {
        if tag.is_none() && cost(1.1) + weight == selected_cost {
            tag = Some(match sign {
                Some(sign) => format!("cardinal {{ negative: \"{sign}\" integer: \"{value}\" }}"),
                None => format!("cardinal {{ integer: \"{value}\" }}"),
            });
        }
    });
    tag
}

// ---------------------------------------------------------------------------
// 1. Cardinal
// ---------------------------------------------------------------------------

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

pub fn classify_cardinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (sign, rest) = parse_sign(s);
    if rest.is_empty() {
        return;
    }

    let top_weight = cost(1.1);
    let sign_tag = match sign {
        Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
        None => String::new(),
    };

    // Range: num + ("-" | "–") + num
    for dash in ["-", "–"] {
        if let Some((left, right)) = rest.split_once(dash) {
            let left = left.trim_end_matches(' ');
            let right = right.trim_start_matches(' ');
            let left_r = numbers::graph(left);
            let right_r = numbers::graph(right);
            for (l_w, l_c) in &left_r {
                for (r_w, r_c) in &right_r {
                    let sign_tag_c = sign_tag.clone();
                    let l_w_c = l_w.clone();
                    let r_w_c = r_w.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + l_c + r_c,
                        rank: 1,
                        tag: super::lazy::tag(move || {
                            format!("cardinal {{ {sign_tag_c}integer: \"{l_w_c} থেকে {r_w_c}\" }}")
                        }),
                    });
                }
            }
        }
    }

    // Arithmetic expression: + (" যোগ "), = (" সমান "), × (" গুণ "), ÷ (" ভাগ ")
    for (op, op_w) in [("+", " যোগ "), ("=", " সমান "), ("×", " গুণ "), ("÷", " ভাগ ")] {
        if let Some((left, right)) = rest.split_once(op) {
            let left = left.trim_end_matches(' ');
            let right = right.trim_start_matches(' ');
            let left_r = numbers::graph(left);
            let right_r = numbers::graph(right);
            for (l_w, l_c) in &left_r {
                for (r_w, r_c) in &right_r {
                    let sign_tag_c = sign_tag.clone();
                    let l_w_c = l_w.clone();
                    let r_w_c = r_w.clone();
                    let op_w_c = op_w;
                    out.push(Candidate {
                        end,
                        cost: top_weight + l_c + r_c,
                        rank: 1,
                        tag: super::lazy::tag(move || {
                            format!("cardinal {{ {sign_tag_c}integer: \"{l_w_c}{op_w_c}{r_w_c}\" }}")
                        }),
                    });
                }
            }
        }
    }

    // Classifier glued to cardinal: ৫টি -> পাঁচটি, ৩জন -> তিনজন
    for &cls in CLASSIFIERS.iter() {
        if let Some(prefix) = rest.strip_suffix(cls) {
            numbers::for_each_graph(prefix, &mut |val, w| {
                out.push(Candidate {
                    end,
                    cost: top_weight + w,
                    rank: 1,
                    tag: super::lazy::tag(|| format!("cardinal {{ {sign_tag}integer: \"{val}{cls}\" }}")),
                });
            });
        }
    }

    // Plain cardinal
    numbers::for_each_graph(rest, &mut |val, w| {
        out.push(Candidate {
            end,
            cost: top_weight + w,
            rank: 1,
            tag: super::lazy::tag(|| format!("cardinal {{ {sign_tag}integer: \"{val}\" }}")),
        });
    });
}

// ---------------------------------------------------------------------------
// 2. Decimal
// ---------------------------------------------------------------------------

pub fn classify_decimal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (sign, rest) = parse_sign(s);
    if rest.is_empty() {
        return;
    }

    let top_weight = cost(1.1);
    let sign_tag = match sign {
        Some(v) => super::lazy::tag(|| format!("negative: \"{v}\" ")),
        None => String::new(),
    };

    // Case 1: cardinal + quantity, e.g. "১ লাখ", "২ কোটি"
    for &qty in QUANTITIES.iter() {
        if let Some(prefix) = rest.strip_suffix(qty) {
            let num_str = prefix.trim_end_matches(' ');
            if !num_str.is_empty() {
                numbers::for_each_graph(num_str, &mut |int_val, int_cost| {
                    out.push(Candidate {
                        end,
                        cost: top_weight + int_cost,
                        rank: 2,
                        tag: super::lazy::tag(|| {
                            format!("decimal {{ {sign_tag}integer_part: \"{int_val}\" quantity: \"{qty}\" }}")
                        }),
                    });
                });
            }
        }
    }

    // Case 2: decimal with dot, e.g. "৩.১৪", "0.5", "১.৫ কোটি"
    let (num_part, qty_opt) =
        match QUANTITIES.iter().find_map(|&qty| rest.strip_suffix(qty).map(|p| (p.trim_end_matches(' '), Some(qty)))) {
            Some((p, q)) => (p, q),
            None => (rest, None),
        };

    if let Some((int_s, frac_s)) = num_part.split_once('.') {
        if !int_s.is_empty() && !frac_s.is_empty() && frac_s.chars().all(numbers::is_digit) {
            let frac_val = match numbers::digitwise(frac_s) {
                Some(f) => f,
                None => return,
            };
            numbers::for_each_graph(int_s, &mut |int_val, int_cost| {
                out.push(Candidate {
                    end,
                    cost: top_weight + int_cost,
                    rank: 2,
                    tag: super::lazy::tag(|| {
                        let qty_part = match qty_opt {
                            Some(q) => format!(" quantity: \"{q}\""),
                            None => String::new(),
                        };
                        format!("decimal {{ {sign_tag}integer_part: \"{int_val}\" fractional_part: \"{frac_val}\"{qty_part} }}")
                    }),
                });
            });
        }
    }
}

// ---------------------------------------------------------------------------
// 3. Fraction
// ---------------------------------------------------------------------------

const VULGAR_OTHER: &[(&str, &str, &str)] = &[
    ("⅐", "এক", "সাত"),
    ("⅑", "এক", "নয়"),
    ("⅒", "এক", "দশ"),
    ("⅕", "এক", "পাঁচ"),
    ("⅖", "দুই", "পাঁচ"),
    ("⅗", "তিন", "পাঁচ"),
    ("⅘", "চার", "পাঁচ"),
    ("⅙", "এক", "ছয়"),
    ("⅚", "পাঁচ", "ছয়"),
    ("⅛", "এক", "আট"),
    ("⅜", "তিন", "আট"),
    ("⅝", "পাঁচ", "আট"),
    ("⅞", "সাত", "আট"),
];

const VULGAR_MIXED_ONLY: &[(&str, &str, &str)] = &[("⅓", "এক", "তিন"), ("⅔", "দুই", "তিন")];

pub fn classify_fraction(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (sign, rest) = parse_sign(s);
    if rest.is_empty() {
        return;
    }
    // Fraction only allows negative: "true"
    let sign_tag = if sign == Some("true") { "negative: \"true\" " } else { "" };
    let top_weight = cost(1.05);

    // 1. Standalone common fraction (weight -0.3)
    for (k, v) in COMMON_FRACTIONS.iter() {
        if rest == *k {
            let sign_tag_c = sign_tag;
            out.push(Candidate {
                end,
                cost: top_weight + cost(-0.3),
                rank: 3,
                tag: super::lazy::tag(move || format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"{v}\" }}")),
            });
        }
    }

    // 2. Dedh / Arai (weight -0.2)
    // "1 1/2", "১ ১/২", "1½", "১½" -> দেড়
    if matches!(rest, "1 1/2" | "১ ১/২" | "1 1/২" | "১ 1/2" | "1½" | "১½" | "1 ½" | "১ ½") {
        let sign_tag_c = sign_tag;
        out.push(Candidate {
            end,
            cost: top_weight + cost(-0.2),
            rank: 3,
            tag: super::lazy::tag(move || format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"দেড়\" }}")),
        });
    }
    // "2 1/2", "২ ১/২", "2½", "২½" -> আড়াই
    if matches!(rest, "2 1/2" | "২ ১/২" | "2 1/২" | "২ 1/2" | "2½" | "২½" | "2 ½" | "২ ½") {
        let sign_tag_c = sign_tag;
        out.push(Candidate {
            end,
            cost: top_weight + cost(-0.2),
            rank: 3,
            tag: super::lazy::tag(move || format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"আড়াই\" }}")),
        });
    }

    // 3. Poune (weight -0.2): n + 3/4 -> পৌনে <next>
    for sfx in [" 3/4", " ৩/৪", "¾", " ¾"] {
        if let Some(prefix) = rest.strip_suffix(sfx) {
            let prefix = prefix.trim_end_matches(' ');
            if let Some(&next_w) = NEXT_NUMBER.get(prefix) {
                let sign_tag_c = sign_tag;
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.2),
                    rank: 3,
                    tag: super::lazy::tag(move || {
                        format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"পৌনে {next_w}\" }}")
                    }),
                });
            }
        }
    }

    // 4. Soya (weight -0.1): n + 1/4 -> সোয়া <n>
    for sfx in [" 1/4", " ১/৪", "¼", " ¼"] {
        if let Some(prefix) = rest.strip_suffix(sfx) {
            let prefix = prefix.trim_end_matches(' ');
            for (card_w, card_c) in numbers::graph(prefix) {
                let sign_tag_c = sign_tag;
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.1) + card_c,
                    rank: 3,
                    tag: super::lazy::tag(move || {
                        format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"সোয়া {card_w}\" }}")
                    }),
                });
            }
        }
    }

    // 5. Sare (weight -0.1): n + 1/2 -> সাড়ে <n> (for n >= 3)
    for sfx in [" 1/2", " ১/২", "½", " ½"] {
        if let Some(prefix) = rest.strip_suffix(sfx) {
            let prefix = prefix.trim_end_matches(' ');
            // dedh (1) and arai (2) handled above with weight -0.2
            for (card_w, card_c) in numbers::graph(prefix) {
                let sign_tag_c = sign_tag;
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.1) + card_c,
                    rank: 3,
                    tag: super::lazy::tag(move || {
                        format!("fraction {{ {sign_tag_c}morphosyntactic_features: \"সাড়ে {card_w}\" }}")
                    }),
                });
            }
        }
    }

    // 6. Vulgar other standalone
    for &(ch, num_w, den_w) in VULGAR_OTHER {
        if rest == ch {
            let sign_tag_c = sign_tag;
            out.push(Candidate {
                end,
                cost: top_weight,
                rank: 3,
                tag: super::lazy::tag(move || {
                    format!("fraction {{ {sign_tag_c}numerator: \"{num_w}\" denominator: \"{den_w}\" }}")
                }),
            });
        }
    }

    // 7. Vulgar mixed: integer + (vulgar_other | vulgar_mixed_only)
    for &(ch, num_w, den_w) in VULGAR_OTHER.iter().chain(VULGAR_MIXED_ONLY.iter()) {
        if let Some(prefix) = rest.strip_suffix(ch) {
            let prefix = prefix.trim_end_matches(' ');
            if !prefix.is_empty() {
                for (int_w, int_c) in numbers::graph(prefix) {
                    let sign_tag_c = sign_tag;
                    out.push(Candidate {
                        end,
                        cost: top_weight + int_c,
                        rank: 3,
                        tag: super::lazy::tag(move || {
                            format!("fraction {{ {sign_tag_c}integer_part: \"{int_w}\" numerator: \"{num_w}\" denominator: \"{den_w}\" }}")
                        }),
                    });
                }
            }
        }
    }

    // 8. General fraction: [integer + " "] numerator / denominator
    if let Some((left, den_s)) = rest.split_once('/') {
        let den_s = den_s.trim_start_matches(' ');
        let den_readings = numbers::graph(den_s);
        if den_readings.is_empty() {
            return;
        }

        let left = left.trim_end_matches(' ');
        // Check if there is an integer part: "২৩ ৪" -> integer="২৩", num="৪"
        let (int_opt, num_s) = match left.rsplit_once(' ') {
            Some((i_part, n_part)) if !i_part.is_empty() && !n_part.is_empty() => (Some(i_part), n_part),
            _ => (None, left),
        };

        let num_readings = numbers::graph(num_s);
        if num_readings.is_empty() {
            return;
        }

        if let Some(int_s) = int_opt {
            let int_readings = numbers::graph(int_s);
            for (int_w, int_c) in &int_readings {
                for (num_w, num_c) in &num_readings {
                    for (den_w, den_c) in &den_readings {
                        let sign_tag_c = sign_tag;
                        let int_w_c = int_w.clone();
                        let num_w_c = num_w.clone();
                        let den_w_c = den_w.clone();
                        out.push(Candidate {
                            end,
                            cost: top_weight + int_c + num_c + den_c,
                            rank: 3,
                            tag: super::lazy::tag(move || {
                                format!("fraction {{ {sign_tag_c}integer_part: \"{int_w_c}\" numerator: \"{num_w_c}\" denominator: \"{den_w_c}\" }}")
                            }),
                        });
                    }
                }
            }
        } else {
            for (num_w, num_c) in &num_readings {
                for (den_w, den_c) in &den_readings {
                    let sign_tag_c = sign_tag;
                    let num_w_c = num_w.clone();
                    let den_w_c = den_w.clone();
                    out.push(Candidate {
                        end,
                        cost: top_weight + num_c + den_c,
                        rank: 3,
                        tag: super::lazy::tag(move || {
                            format!("fraction {{ {sign_tag_c}numerator: \"{num_w_c}\" denominator: \"{den_w_c}\" }}")
                        }),
                    });
                }
            }
        }
    }
}

// ---------------------------------------------------------------------------
// 4. Ordinal
// ---------------------------------------------------------------------------

pub fn classify_ordinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    // 1. Ordinal exceptions table (১ম -> প্রথম, 1st -> প্রথম) (weight -0.1)
    if let Some(&word) = ORDINAL_EXCEPTIONS.get(s) {
        out.push(Candidate {
            end,
            cost: top_weight + cost(-0.1),
            rank: 9,
            tag: super::lazy::tag(move || format!("ordinal {{ integer: \"{word}\" }}")),
        });
    }

    // 2. Calendar day ordinals (১লা -> পয়লা, ২১শে -> একুশে) (weight -0.1)
    if let Some(&word) = DAY_ORDINAL.get(s) {
        out.push(Candidate {
            end,
            cost: top_weight + cost(-0.1),
            rank: 9,
            tag: super::lazy::tag(move || format!("ordinal {{ integer: \"{word}\" }}")),
        });
    }

    // 3. Regular ordinal: cardinal + "তম"
    if let Some(prefix) = s.strip_suffix("তম") {
        for (val, c) in numbers::graph(prefix) {
            out.push(Candidate {
                end,
                cost: top_weight + c,
                rank: 9,
                tag: super::lazy::tag(move || format!("ordinal {{ integer: \"{val}তম\" }}")),
            });
        }
    }

    // 4. English suffixes: 21st, 102nd -> <cardinal>তম (weight -0.1)
    for sfx in ["st", "nd", "rd", "th"] {
        if let Some(prefix) = s.strip_suffix(sfx) {
            for (val, c) in numbers::graph(prefix) {
                out.push(Candidate {
                    end,
                    cost: top_weight + c + cost(-0.1),
                    rank: 9,
                    tag: super::lazy::tag(move || format!("ordinal {{ integer: \"{val}তম\" }}")),
                });
            }
        }
    }
}
