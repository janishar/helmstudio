//! Marathi measurements, fractional units, dimensions and address bindings.
use super::{address, numbers, numeric};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const UNIT: &str = include_str!("../../../data/mr/measure/unit.tsv");
const FORMAL: &str = include_str!("../../../data/mr/measure/unit_year_formal.tsv");
const QUARTER1: &str = include_str!("../../../data/mr/measure/quarterly_units_map.tsv");
const QUARTER2: &str = include_str!("../../../data/mr/measure/quarterly_units_list.tsv");
const PAUNE: &str = include_str!("../../../data/mr/whitelist/paune_mappings.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

pub(crate) fn number_char(c: char) -> bool {
    if numeric::structural_char(c) || matches!(c, '—' | 'x' | 'X' | '*' | '\t' | '\n' | '\r' | '\u{a0}') {
        return true;
    }
    struct Alphabet {
        ascii: [bool; 128],
        devanagari: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ALPHABET: LazyLock<Alphabet> = LazyLock::new(|| {
        let mut a = Alphabet { ascii: [false; 128], devanagari: [false; 128], other: Default::default() };
        let keys = rows(UNIT)
            .chain(rows(FORMAL))
            .chain(rows(QUARTER1))
            .chain(rows(include_str!("../../../data/mr/numbers/thousands.tsv")))
            .map(|(key, _)| key)
            .chain(QUARTER2.lines().map(|line| line.trim_end_matches('\r')));
        for c in keys.flat_map(str::chars) {
            let n = c as usize;
            if n < 128 {
                a.ascii[n] = true;
            } else if (0x0900..=0x097f).contains(&n) {
                a.devanagari[n - 0x0900] = true;
            } else {
                a.other.insert(c);
            }
        }
        a
    });
    let n = c as usize;
    if n < 128 {
        ALPHABET.ascii[n]
    } else if (0x0900..=0x097f).contains(&n) {
        ALPHABET.devanagari[n - 0x0900]
    } else {
        ALPHABET.other.contains(&c)
    }
}

type UnitRow = (&'static str, &'static str);
struct UnitIndex {
    ascii: [Vec<UnitRow>; 128],
    devanagari: [Vec<UnitRow>; 128],
    other: std::collections::HashMap<char, Vec<UnitRow>>,
}

impl UnitIndex {
    fn get(&self, c: char) -> &[UnitRow] {
        let n = c as usize;
        if n < 128 {
            &self.ascii[n]
        } else if (0x0900..=0x097f).contains(&n) {
            &self.devanagari[n - 0x0900]
        } else {
            self.other.get(&c).map(Vec::as_slice).unwrap_or(&[])
        }
    }
}

fn unit_index(rows: impl Iterator<Item = UnitRow>) -> UnitIndex {
    let mut out = UnitIndex {
        ascii: std::array::from_fn(|_| Vec::new()),
        devanagari: std::array::from_fn(|_| Vec::new()),
        other: Default::default(),
    };
    for (key, value) in rows {
        if let Some(last) = key.chars().last() {
            let n = last as usize;
            if n < 128 {
                out.ascii[n].push((key, value));
            } else if (0x0900..=0x097f).contains(&n) {
                out.devanagari[n - 0x0900].push((key, value));
            } else {
                out.other.entry(last).or_default().push((key, value));
            }
        }
    }
    out
}

static UNITS: LazyLock<UnitIndex> = LazyLock::new(|| unit_index(rows(UNIT).chain(rows(FORMAL))));

static QUARTERS: LazyLock<UnitIndex> = LazyLock::new(|| {
    unit_index(rows(QUARTER1).chain(QUARTER2.lines().map(|line| {
        let word = line.trim_end_matches('\r');
        (word, word)
    })))
});

pub(crate) fn possible_unit_end(c: char) -> bool {
    !UNITS.get(c).is_empty() || !QUARTERS.get(c).is_empty()
}

static PAUNE_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(PAUNE) {
        out.insert(k, v);
    }
    out
});

fn sign(s: &str) -> (&str, Option<&'static str>) {
    if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (r, Some("true"))
    } else if let Some(r) = s.strip_prefix('~') {
        (r.strip_prefix(' ').unwrap_or(r), Some("सुमारे"))
    } else if let Some(r) = s.strip_prefix('±') {
        (r.strip_prefix(' ').unwrap_or(r), Some("अधिक किंवा उणे"))
    } else {
        (s, None)
    }
}

fn signed(s: Option<&str>) -> String {
    s.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}

fn range_val(s: &str) -> Vec<(String, Cost)> {
    if let Some((a, b)) = s.split_once('.') {
        if let Some(frac) = numbers::digitwise(b) {
            return numbers::graph(a)
                .into_iter()
                .map(|(n, c)| (super::lazy::tag(|| format!("{n} दशांश {frac}")), c))
                .collect();
        }
    }
    numbers::graph(s)
}

pub fn city_prefix_end(s: &str) -> Option<usize> {
    address::city_prefix_end(s)
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    classify_hinted(
        s,
        end,
        out,
        city_prefix_end(s).is_some(),
        address::keyword_bounds(s).is_some(),
        s.contains(|c| matches!(c, 'x' | 'X' | '*')),
    );
}

pub fn classify_hinted(
    s: &str,
    end: usize,
    out: &mut Vec<Candidate>,
    city_possible: bool,
    address_keyword: bool,
    has_dimension: bool,
) {
    if city_possible {
        address::classify_structured(s, end, out);
    }
    if address_keyword {
        address::classify_keyword(s, end, out);
    }
    if has_dimension {
        classify_dimension(s, end, out);
    }
    classify_measure_units(s, end, out);
}

fn classify_dimension(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (rem, sgn) = sign(s);
    for sep in ['x', 'X', '*'] {
        if let Some((a, b)) = rem.split_once(sep) {
            let a = a.trim();
            let b = b.trim();
            for (v1, c1) in numbers::graph(a) {
                for (v2, c2) in numbers::graph(b) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1 + 0.1) + c1 + c2,
                        rank: 6,
                        tag: super::lazy::tag(|| {
                            let s = signed(sgn);
                            format!("measure {{ cardinal {{ {s}integer: \"{v1}\" }} units: \"बाय\" }} }} tokens {{ cardinal {{ {s}integer: \"{v2}\"")
                        }),
                    });
                }
            }
        }
    }
}

fn classify_measure_units(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(number) = s.strip_suffix('s') {
        let mut digits = number.chars();
        let first = digits.next();
        let count = number.chars().count();
        if (count == 4 && matches!(first, Some('1' | '2' | '१' | '२')))
            || (count == 2 && first.is_some_and(|c| numbers::digit(c).is_some() && !numbers::is_zero(c)))
        {
            if number.chars().all(|c| numbers::digit(c).is_some())
                && number.chars().last().is_some_and(numbers::is_zero)
            {
                return;
            }
        }
    }
    let Some(last_char) = s.chars().last() else { return };
    let (rem, sgn) = sign(s);

    // 1. Standard Units
    for &(unit_key, unit_spoken) in UNITS.get(last_char) {
        if let Some(prefix) = rem.strip_suffix(unit_key) {
            let num_str = prefix.strip_suffix(' ').unwrap_or(prefix);
            if num_str.is_empty() {
                continue;
            }

            let is_yr = unit_key == "yr";

            // Check range: "10-20", "10–20", "10—20", etc.
            let range_split = [" - ", " – ", " — ", "-", "–", "—"].into_iter().find_map(|sep| num_str.split_once(sep));

            if let Some((r1, r2)) = range_split {
                let r1 = r1.trim();
                let r2 = r2.trim();
                for (v1, c1) in range_val(r1) {
                    for (v2, c2) in range_val(r2) {
                        out.push(Candidate {
                            end,
                            cost: cost(1.1 - 0.05) + c1 + c2,
                            rank: 6,
                            tag: super::lazy::tag(|| {
                                let s = signed(sgn);
                                format!(
                                    "measure {{ cardinal {{ {s}integer: \"{v1} ते {v2}\" }} units: \"{unit_spoken}\" }}"
                                )
                            }),
                        });
                    }
                }
                continue;
            }

            // Decimal measure
            if let Some((int_part, frac_part)) = num_str.split_once('.') {
                if frac_part.chars().all(|c| numbers::digit(c).is_some()) {
                    if let Some(frac_words) = numbers::digitwise(frac_part) {
                        for (int_word, c) in numbers::graph(int_part) {
                            let w = if is_yr { -0.05 } else { -0.05 };
                            out.push(Candidate {
                                end,
                                cost: cost(1.1 + w) + c,
                                rank: 6,
                                tag: super::lazy::tag(|| {
                                    let s = signed(sgn);
                                    format!("measure {{ decimal {{ {s}integer_part: \"{int_word}\" fractional_part: \"{frac_words}\" }} units: \"{unit_spoken}\" }}")
                                }),
                            });
                        }
                    }
                }
                continue;
            }

            // Cardinal measure
            let count = num_str.chars().count();
            for (card_word, c) in numbers::graph(num_str) {
                let w = if is_yr { if count >= 4 { -0.05 } else { -0.1 } } else { -0.05 };
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + w) + c,
                    rank: 6,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("measure {{ cardinal {{ {s}integer: \"{card_word}\" }} units: \"{unit_spoken}\" }}")
                    }),
                });
            }
        }
    }

    // 2. Quarterly Units
    for &(unit_key, unit_spoken) in QUARTERS.get(last_char) {
        if let Some(prefix) = rem.strip_suffix(unit_key) {
            let num_str = prefix.strip_suffix(' ').unwrap_or(prefix);
            if num_str.is_empty() {
                continue;
            }

            // Dedh / Dhai: 1.5, १.५, 2.5, २.५
            if num_str == "1.5" || num_str == "१.५" {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 - 0.2),
                    rank: 6,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("measure {{ cardinal {{ {s}integer: \"दीड\" }} units: \"{unit_spoken}\" }}")
                    }),
                });
            } else if num_str == "2.5" || num_str == "२.५" {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 - 0.2),
                    rank: 6,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("measure {{ cardinal {{ {s}integer: \"अडीच\" }} units: \"{unit_spoken}\" }}")
                    }),
                });
            }

            // Savva: .25, .२५
            if let Some(base) = num_str.strip_suffix(".25").or_else(|| num_str.strip_suffix(".२५")) {
                let base = base.strip_suffix(' ').unwrap_or(base);
                for (card_word, c) in numbers::graph(base) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1 - 0.1) + c,
                        rank: 6,
                        tag: super::lazy::tag(|| {
                            let s = signed(sgn);
                            format!(
                                "measure {{ cardinal {{ {s}integer: \"सव्वा {card_word}\" }} units: \"{unit_spoken}\" }}"
                            )
                        }),
                    });
                }
            }

            // Sadhe: .5, .५
            if let Some(base) = num_str.strip_suffix(".5").or_else(|| num_str.strip_suffix(".५")) {
                let base = base.strip_suffix(' ').unwrap_or(base);
                if base != "1" && base != "१" && base != "2" && base != "२" {
                    for (card_word, c) in numbers::graph(base) {
                        out.push(Candidate {
                            end,
                            cost: cost(1.1 - 0.1) + c,
                            rank: 6,
                            tag: super::lazy::tag(|| {
                                let s = signed(sgn);
                                format!("measure {{ cardinal {{ {s}integer: \"साडे {card_word}\" }} units: \"{unit_spoken}\" }}")
                            }),
                        });
                    }
                }
            }

            // Paune: .75, .७५
            if let Some(base) = num_str.strip_suffix(".75").or_else(|| num_str.strip_suffix(".७५")) {
                let base = base.strip_suffix(' ').unwrap_or(base);
                if let Some(&mapped) = PAUNE_MAP.get(base) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1 - 0.5),
                        rank: 6,
                        tag: super::lazy::tag(|| {
                            let s = signed(sgn);
                            format!(
                                "measure {{ cardinal {{ {s}integer: \"पावणे {mapped}\" }} units: \"{unit_spoken}\" }}"
                            )
                        }),
                    });
                }
            }
        }
    }
}
