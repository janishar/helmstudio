//! Bengali Telephone classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;

pub fn possible_start(input: &str) -> bool {
    let first = match input.chars().next() {
        Some(c) => c,
        None => return false,
    };
    first == '+' || first == '(' || numbers::zero(first)
}

pub fn plain_char(c: char) -> bool {
    numbers::is_digit(c) || matches!(c, '+' | '(' | ')' | ' ' | '-' | '.')
}

fn digit_word(c: char) -> Option<&'static str> {
    numbers::digit(c)
}

fn digit_seq(s: &str) -> Option<String> {
    let mut out = String::new();
    for c in s.chars() {
        if matches!(c, ' ' | '-' | '.') {
            continue;
        }
        let w = digit_word(c)?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(w);
    }
    Some(out)
}

fn count_digits(s: &str) -> usize {
    s.chars().filter(|&c| numbers::is_digit(c)).count()
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    classify_hinted(s, end, out, false);
}

pub fn classify_hinted(s: &str, end: usize, out: &mut Vec<Candidate>, _phone_plain: bool) {
    let top_weight = cost(1.1);

    // 1. Bracketed area code: (০২) ৯৮৭৬৫৪৩
    if let Some(rest) = s.strip_prefix('(') {
        if let Some((area, num)) = rest.split_once(')') {
            let area_digits = count_digits(area);
            let num_digits = count_digits(num);
            if (2..=5).contains(&area_digits) && (5..=8).contains(&num_digits) {
                let first_c = area.chars().find(|&c| numbers::is_digit(c));
                if first_c.is_some_and(numbers::zero) {
                    if let (Some(area_w), Some(num_w)) = (digit_seq(area), digit_seq(num)) {
                        out.push(Candidate {
                            end,
                            cost: top_weight,
                            rank: 8,
                            tag: super::lazy::tag(move || format!("telephone {{ number_part: \"{area_w} {num_w}\" }}")),
                        });
                        return;
                    }
                }
            }
        }
    }

    // 2. Country code prefix: +880 1712-345678, +1-555-123-4567
    if let Some(rest) = s.strip_prefix('+') {
        // Country code is 1..3 digits, optionally followed by space or hyphen
        let chars: Vec<char> = rest.chars().collect();
        for cc_len in 1..=3 {
            if cc_len > chars.len() {
                break;
            }
            let cc_part: String = chars[..cc_len].iter().collect();
            if cc_part.chars().all(numbers::is_digit) {
                let after_cc = &rest[cc_part.len()..];
                let after_cc = after_cc.strip_prefix(' ').or_else(|| after_cc.strip_prefix('-')).unwrap_or(after_cc);
                let num_digits = count_digits(after_cc);
                if (6..=12).contains(&num_digits) {
                    if let (Some(cc_w), Some(num_w)) = (digit_seq(&cc_part), digit_seq(after_cc)) {
                        out.push(Candidate {
                            end,
                            cost: top_weight,
                            rank: 8,
                            tag: super::lazy::tag(move || {
                                format!("telephone {{ country_code: \"প্লাস {cc_w}\" number_part: \"{num_w}\" }}")
                            }),
                        });
                    }
                }
            }
        }
    }

    // 3. National number (starts with 0 or ০, 9..11 total digits)
    let first_c = s.chars().next();
    if first_c.is_some_and(numbers::zero) {
        let total_digits = count_digits(s);
        if (9..=11).contains(&total_digits) {
            // Check that it's composed only of digits and separators
            if s.chars().all(|c| numbers::is_digit(c) || matches!(c, ' ' | '-' | '.')) {
                if let Some(num_w) = digit_seq(s) {
                    out.push(Candidate {
                        end,
                        cost: top_weight,
                        rank: 8,
                        tag: super::lazy::tag(move || format!("telephone {{ number_part: \"{num_w}\" }}")),
                    });
                }
            }
        }
    }
}
