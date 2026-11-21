use super::number;

const FIXED: &[(&str, &str, i64)] = &[
    ("Dr. ", "ดร. ", 50_000),
    ("Dr.", "ดร.", 50_000),
    ("dr. ", "ดร. ", 50_000),
    ("dr.", "ดร.", 50_000),
    ("ดร.", "ดอกเตอร์", 50_000),
    ("Mr. ", "นาย ", 50_000),
    ("Mr.", "นาย", 50_000),
    ("Mrs. ", "นาง ", 50_000),
    ("Mrs.", "นาง", 50_000),
    ("Miss ", "นางสาว ", 50_000),
    ("Prof. ", "ศ. ", 50_000),
    ("Prof.", "ศ.", 50_000),
    ("&", "และ", 60_000),
    ("etc.", "เป็นต้น", 60_000),
    ("ฯลฯ", "เป็นต้น", 60_000),
    ("5G", "ห้าจี", 30_000),
    ("5g", "ห้าจี", 30_000),
    ("4G", "สี่จี", 30_000),
    ("4g", "สี่จี", 30_000),
    ("3G", "สามจี", 30_000),
    ("3g", "สามจี", 30_000),
    ("4K", "สี่เค", 30_000),
    ("4k", "สี่เค", 30_000),
    ("8K", "แปดเค", 30_000),
    ("8k", "แปดเค", 30_000),
    ("CO2", "CO สอง", 30_000),
    ("co2", "co สอง", 30_000),
    ("H2O", "H สอง O", 30_000),
    ("h2o", "h สอง o", 30_000),
    ("COVID-19", "COVID สิบเก้า", 30_000),
    ("covid-19", "covid สิบเก้า", 30_000),
    ("Covid-19", "Covid สิบเก้า", 30_000),
    ("A4", "A สี่", 30_000),
    ("a4", "a สี่", 30_000),
];

pub(crate) fn parse(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for &(input, word, cost) in FIXED {
        if s == input {
            out.push((word.to_owned(), cost));
        }
    }
    for p in ["No. ", "No.", "no. ", "no.", "Nr. ", "Nr.", "#"] {
        if let Some(x) = s.strip_prefix(p) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(n) = number::positive(x) {
                out.push((format!("หมายเลข{n}"), 40_000));
            }
        }
    }
    for (p, w) in [("©", "ลิขสิทธิ์"), ("§", "มาตรา")] {
        if let Some(x) = s.strip_prefix(p) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            if x.is_empty() {
                out.push((w.to_owned(), 40_000));
            } else if let Some(n) = number::positive(x) {
                out.push((format!("{w} {n}"), 40_000));
            }
        }
    }
    for (p, w) in
        [("iOS ", "iOS "), ("ios ", "ios "), ("v", "v "), ("V", "V "), ("ver ", "เวอร์ชัน "), ("Ver ", "เวอร์ชัน ")]
    {
        if let Some(x) = s.strip_prefix(p) {
            if let Some((major, rest)) = x.split_once('.') {
                if rest.split('.').all(|p| p.chars().count() == 1 && p.chars().all(|c| number::digit(c).is_some())) {
                    if let Some(n) = number::positive(major) {
                        let mut v = format!("{w}{n}");
                        for p in rest.split('.') {
                            v.push_str("จุด");
                            v.push_str(&number::digit_words(p).unwrap());
                        }
                        out.push((v, 10_000));
                    }
                }
            }
        }
    }
    let mut parts = s.split('-');
    if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
        if (1..=3).contains(&a.len())
            && (1..=3).contains(&c.len())
            && a.chars().all(|c| c.is_ascii_alphabetic())
            && c.chars().all(|c| c.is_ascii_alphabetic())
            && (1..=4).contains(&b.chars().count())
        {
            if let Some(n) = number::digit_words(b) {
                out.push((format!("{a} {n} {c}"), 20_000));
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        let base = if a.len() == 1 && a.chars().all(|c| c.is_ascii_alphabetic()) {
            Some(a.to_owned())
        } else {
            number::positive(a)
        };
        if let (Some(a), Some(b)) = (base, number::positive(b)) {
            out.push((format!("{a} ยกกำลัง{b}"), 20_000));
        }
    }
    out
}
