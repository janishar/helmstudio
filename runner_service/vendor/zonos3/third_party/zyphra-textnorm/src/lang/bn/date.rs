//! Bengali Date classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const DAYS_DATA: &str = include_str!("../../../data/bn/date/days.tsv");
const DAY_ORDINAL_DATA: &str = include_str!("../../../data/bn/date/day_ordinal.tsv");
const MONTHS_DATA: &str = include_str!("../../../data/bn/date/months.tsv");
const MONTH_NAMES_DATA: &str = include_str!("../../../data/bn/date/month_names.tsv");
const MONTH_ABBR_DATA: &str = include_str!("../../../data/bn/date/month_abbr.tsv");
const YEAR_SUFFIX_DATA: &str = include_str!("../../../data/bn/date/year_suffix.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static DAYS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(DAYS_DATA).collect());
static DAY_ORDINAL: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(DAY_ORDINAL_DATA).collect());
static MONTHS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(MONTHS_DATA).collect());
static MONTH_NAMES: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| MONTH_NAMES_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());
static MONTH_ABBR: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(MONTH_ABBR_DATA).collect());
static YEAR_SUFFIX: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(YEAR_SUFFIX_DATA).collect());

type Entries = std::collections::HashMap<char, Vec<(&'static str, &'static str)>>;
static MONTH_NAME_INDEX: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = std::collections::HashMap::<char, Vec<&'static str>>::new();
    for &name in MONTH_NAMES.iter() {
        if let Some(c) = name.chars().next() {
            index.entry(c).or_default().push(name);
        }
    }
    index
});
static MONTH_ABBR_INDEX: LazyLock<Entries> = LazyLock::new(|| {
    let mut index = Entries::new();
    for (&key, &value) in MONTH_ABBR.iter() {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
});
static DAY_INDEX: LazyLock<Entries> = LazyLock::new(|| {
    let mut index = Entries::new();
    for (&key, &value) in DAYS.iter().chain(DAY_ORDINAL.iter()) {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
});

static DATE_ENDS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    MONTH_NAMES
        .iter()
        .copied()
        .chain(MONTH_ABBR.keys().copied())
        .chain(YEAR_SUFFIX.keys().copied())
        .chain(["s", "'s", "-এর দশক", " এর দশক", "এর দশক", "."])
        .filter_map(|key| key.chars().next_back())
        .collect()
});

const DECADE_TAIL: &[(&str, &str)] = &[
    ("00", "শোয়ের দশক"),
    ("০০", "শোয়ের দশক"),
    ("10", " দশের দশক"),
    ("১০", " দশের দশক"),
    ("20", " বিশের দশক"),
    ("২০", " বিশের দশক"),
    ("30", " ত্রিশের দশক"),
    ("৩০", " ত্রিশের দশক"),
    ("40", " চল্লিশের দশক"),
    ("৪০", " চল্লিশের দশক"),
    ("50", " পঞ্চাশের দশক"),
    ("৫০", " পঞ্চাশের দশক"),
    ("60", " ষাটের দশক"),
    ("৬০", " ষাটের দশক"),
    ("70", " সত্তরের দশক"),
    ("৭০", " সত্তরের দশক"),
    ("80", " আশির দশক"),
    ("৮০", " আশির দশক"),
    ("90", " নব্বইয়ের দশক"),
    ("৯০", " নব্বইয়ের দশক"),
];

const DECADE_2D_STEM: &[(&str, &str)] = &[
    ("10", "দশের দশক"),
    ("১০", "দশের দশক"),
    ("20", "বিশের দশক"),
    ("২০", "বিশের দশক"),
    ("30", "ত্রিশের দশক"),
    ("৩০", "ত্রিশের দশক"),
    ("40", "চল্লিশের দশক"),
    ("৪০", "চল্লিশের দশক"),
    ("50", "পঞ্চাশের দশক"),
    ("৫০", "পঞ্চাশের দশক"),
    ("60", "ষাটের দশক"),
    ("৬০", "ষাটের দশক"),
    ("70", "সত্তরের দশক"),
    ("৭০", "সত্তরের দশক"),
    ("80", "আশির দশক"),
    ("৮০", "আশির দশক"),
    ("90", "নব্বইয়ের দশক"),
    ("৯০", "নব্বইয়ের দশক"),
];

pub fn possible_start(input: &str) -> bool {
    let first = match input.chars().next() {
        Some(c) => c,
        None => return false,
    };
    numbers::is_digit(first)
        || first == '\''
        || MONTH_NAME_INDEX.get(&first).is_some_and(|entries| entries.iter().any(|m| input.starts_with(m)))
        || MONTH_ABBR_INDEX.get(&first).is_some_and(|entries| entries.iter().any(|(m, _)| input.starts_with(m)))
}

pub fn possible_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(c, '/' | '-' | '.' | '–' | ' ' | ',' | '\'' | 's')
        || ('\u{0980}'..='\u{09FF}').contains(&c)
}

pub fn possible_end(s: &str, c: char) -> bool {
    numbers::is_digit(c)
        || DATE_ENDS.contains(&c)
        || (c == ' ' && s.trim_end().chars().next_back().is_some_and(numbers::is_digit))
}

pub fn might_match(s: &str) -> bool {
    if s.chars().next().is_some_and(numbers::is_digit)
        && s.chars().any(|c| ('\u{0980}'..='\u{09FF}').contains(&c) && !numbers::is_digit(c))
    {
        return YEAR_SUFFIX.keys().any(|key| s.ends_with(key))
            || ["-এর দশক", " এর দশক", "এর দশক"].iter().any(|suffix| s.ends_with(suffix))
            || s.split_once(' ').is_some_and(|(day, rest)| {
                (DAYS.contains_key(day) || DAY_ORDINAL.contains_key(day)) && month_name_lookup(rest).is_some()
            });
    }
    s.contains('/')
        || s.contains('-')
        || s.contains('–')
        || s.contains('.')
        || s.contains(' ')
        || s.ends_with('s')
        || YEAR_SUFFIX.keys().any(|k| s.contains(k))
}

fn month_name_lookup(s: &str) -> Option<(&'static str, usize)> {
    let first = s.chars().next()?;
    for &m in MONTH_NAME_INDEX.get(&first).into_iter().flatten() {
        if s.starts_with(m) {
            return Some((m, m.len()));
        }
    }
    for &(abbr, full) in MONTH_ABBR_INDEX.get(&first).into_iter().flatten() {
        if s.starts_with(abbr) {
            let mut len = abbr.len();
            if s[len..].starts_with('.') {
                len += 1;
            }
            return Some((full, len));
        }
    }
    None
}

fn for_each_year(s: &str, era: bool, emit: &mut dyn FnMut(String, Cost)) {
    let count = s.chars().count();
    if (if era { (1..=4).contains(&count) } else { count == 4 })
        && s.chars().all(numbers::is_digit)
        && s.chars().next().is_some_and(|c| !numbers::zero(c))
    {
        numbers::for_each_graph(s, emit);
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    // 1. Decade: native ("1990-এর দশক", "১৯৯০-এর দশক", "1990 এর দশক", "৯০-এর দশক")
    // and international ("2020s", "1990s", "90s", "'90s")
    for &sfx in &["s", "'s", "-এর দশক", " এর দশক", "এর দশক"] {
        if let Some(stem) = s.strip_suffix(sfx) {
            // 2-digit decade: "90", "'90", "৯০"
            let stem_2d = stem.strip_prefix('\'').unwrap_or(stem);
            for &(code, spoken) in DECADE_2D_STEM {
                if stem_2d == code {
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.1),
                        rank: 4,
                        tag: super::lazy::tag(move || format!("date {{ era: \"{spoken}\" }}")),
                    });
                }
            }

            // 4-digit decade: millennium prefix (1, 2, ১, ২) + thousands + hundred + decade_tail
            let mut chars = stem.chars();
            let first = chars.next();
            let second = chars.next();
            let tail_2d = chars.as_str();
            if matches!(first, Some('1' | '2' | '১' | '২')) && tail_2d.chars().count() == 2 {
                let mil_word = match first {
                    Some('1' | '১') => "এক হাজার",
                    Some('2' | '২') => "দুই হাজার",
                    _ => unreachable!(),
                };
                let c1 = second.unwrap();

                if numbers::zero(c1) {
                    // Zero hundred: e.g. 2000s -> "দুই হাজার শোয়ের দশক" or "দুই হাজারয়ের দশক"
                    if tail_2d == "00" || tail_2d == "০০" {
                        let spoken = super::lazy::tag(|| format!("{mil_word}য়ের দশক"));
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(-0.1),
                            rank: 4,
                            tag: super::lazy::tag(move || format!("date {{ era: \"{spoken}\" }}")),
                        });
                        let spoken2 = super::lazy::tag(|| format!("{mil_word} শোয়ের দশক"));
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(-0.1),
                            rank: 4,
                            tag: super::lazy::tag(move || format!("date {{ era: \"{spoken2}\" }}")),
                        });
                    } else {
                        for &(tail_code, tail_spoken) in DECADE_TAIL {
                            if tail_2d == tail_code {
                                let spoken = super::lazy::tag(|| format!("{mil_word}{tail_spoken}"));
                                out.push(Candidate {
                                    end,
                                    cost: top_weight + cost(-0.1),
                                    rank: 4,
                                    tag: super::lazy::tag(move || format!("date {{ era: \"{spoken}\" }}")),
                                });
                            }
                        }
                    }
                } else if let Some(h_digit_word) = numbers::digit(c1) {
                    let hundred_part = super::lazy::tag(|| format!("{h_digit_word}শো"));
                    for &(tail_code, tail_spoken) in DECADE_TAIL {
                        if tail_2d == tail_code {
                            let spoken = super::lazy::tag(|| format!("{mil_word} {hundred_part}{tail_spoken}"));
                            out.push(Candidate {
                                end,
                                cost: top_weight + cost(-0.1),
                                rank: 4,
                                tag: super::lazy::tag(move || format!("date {{ era: \"{spoken}\" }}")),
                            });
                        }
                    }
                }
            }
        }
    }

    // 2. Year range: year1 + ("-" | "–") + year2 (weight -0.005)
    for dash in ["-", "–"] {
        if let Some((y1_s, y2_s)) = s.split_once(dash) {
            for_each_year(y1_s.trim(), false, &mut |y1_w, y1_c| {
                for_each_year(y2_s.trim(), false, &mut |y2_w, y2_c| {
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.005) + y1_c + y2_c,
                        rank: 4,
                        tag: super::lazy::tag(|| format!("date {{ era: \"{y1_w} থেকে {y2_w}\" }}")),
                    });
                });
            });
        }
    }

    // 3. Year + Era: era_year + [space] + era (weight -0.05)
    for (&era_key, &era_val) in YEAR_SUFFIX.iter() {
        if let Some(prefix) = s.strip_suffix(era_key) {
            let y_s = prefix.trim_end_matches(' ');
            for_each_year(y_s, true, &mut |y_w, y_c| {
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-0.05) + y_c,
                    rank: 4,
                    tag: super::lazy::tag(|| format!("date {{ year: \"{y_w}\" era: \"{era_val}\" }}")),
                });
            });
        }
    }

    // 4. Numeric date: day [/-.] month [/-.] year (weight -0.5)
    for sep in ['/', '-', '.'] {
        let mut parts = s.split(sep);
        if let (Some(d_s), Some(m_s), Some(y_s), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let (Some(&d_w), Some(&m_w)) = (DAYS.get(d_s), MONTHS.get(m_s)) {
                for_each_year(y_s, false, &mut |y_w, y_c| {
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.5) + y_c,
                        rank: 4,
                        tag: super::lazy::tag(|| format!("date {{ day: \"{d_w}\" month: \"{m_w}\" year: \"{y_w}\" }}")),
                    });
                });
            }
        }
    }

    // 5. ISO date: year [-/] month [-/] day (weight -0.5)
    for sep in ['-', '/'] {
        let mut parts = s.split(sep);
        if let (Some(y_s), Some(m_s), Some(d_s), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let (Some(&m_w), Some(&d_w)) = (MONTHS.get(m_s), DAYS.get(d_s)) {
                for_each_year(y_s, false, &mut |y_w, y_c| {
                    out.push(Candidate {
                        end,
                        cost: top_weight + cost(-0.5) + y_c,
                        rank: 4,
                        tag: super::lazy::tag(|| format!("date {{ year: \"{y_w}\" month: \"{m_w}\" day: \"{d_w}\" }}")),
                    });
                });
            }
        }
    }

    // 6. Written date: day_written + " " + month_name + [", " | " "] + year + [optional_era]
    // Check if starts with day:
    for &(d_key, d_word) in s.chars().next().and_then(|c| DAY_INDEX.get(&c)).into_iter().flatten() {
        if let Some(rest) = s.strip_prefix(d_key) {
            if let Some(rest_after_sp) = rest.strip_prefix(' ') {
                if let Some((m_word, m_len)) = month_name_lookup(rest_after_sp) {
                    let after_month = &rest_after_sp[m_len..];
                    if after_month.is_empty() {
                        // Graph day-month (weight +0.01)
                        out.push(Candidate {
                            end,
                            cost: top_weight + cost(0.01),
                            rank: 4,
                            tag: super::lazy::tag(move || format!("date {{ day: \"{d_word}\" month: \"{m_word}\" }}")),
                        });
                    } else {
                        // Expect optional comma, then space, then year and optional era
                        let trimmed = after_month.trim_start_matches(',').trim_start_matches(' ');
                        // Check for optional era at end:
                        let (year_str, era_opt) = match YEAR_SUFFIX
                            .iter()
                            .find_map(|(&k, &v)| trimmed.strip_suffix(k).map(|p| (p.trim_end_matches(' '), Some(v))))
                        {
                            Some((p, e)) => (p, e),
                            None => (trimmed, None),
                        };

                        for_each_year(year_str, false, &mut |y_w, y_c| {
                            out.push(Candidate {
                                end,
                                cost: top_weight + cost(-0.5) + y_c,
                                rank: 4,
                                tag: super::lazy::tag(|| {
                                    let era_tag = era_opt.map_or_else(String::new, |e| format!(" era: \"{e}\""));
                                    format!("date {{ day: \"{d_word}\" month: \"{m_word}\" year: \"{y_w}\"{era_tag} }}")
                                }),
                            });
                        });
                    }
                }
            }
        }
    }

    // 7. Month-year: month_name + [", " | " "] + year + [optional_era]
    if let Some((m_word, m_len)) = month_name_lookup(s) {
        let after_month = &s[m_len..];
        if !after_month.is_empty() {
            let trimmed = after_month.trim_start_matches(',').trim_start_matches(' ');
            let (year_str, era_opt) = match YEAR_SUFFIX
                .iter()
                .find_map(|(&k, &v)| trimmed.strip_suffix(k).map(|p| (p.trim_end_matches(' '), Some(v))))
            {
                Some((p, e)) => (p, e),
                None => (trimmed, None),
            };

            for_each_year(year_str, false, &mut |y_w, y_c| {
                out.push(Candidate {
                    end,
                    cost: top_weight + y_c,
                    rank: 4,
                    tag: super::lazy::tag(|| {
                        let era_tag = era_opt.map_or_else(String::new, |e| format!(" era: \"{e}\""));
                        format!("date {{ month: \"{m_word}\" year: \"{y_w}\"{era_tag} }}")
                    }),
                });
            });
        }
    }
}
