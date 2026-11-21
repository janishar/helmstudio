use super::{decimal, number};

struct Currency {
    pre: &'static [&'static str],
    post: &'static [&'static str],
    word: &'static str,
    minor: &'static str,
    decimal_pre: bool,
    decimal_post: bool,
}
const CURRENCIES: &[Currency] = &[
    Currency {
        pre: &["฿", "THB ", "THB"],
        post: &["฿", "THB", "บาท"],
        word: "บาท",
        minor: "สตางค์",
        decimal_pre: true,
        decimal_post: true,
    },
    Currency {
        pre: &["US$", "US $", "USD ", "USD", "$"],
        post: &["$", "USD"],
        word: "ดอลลาร์",
        minor: "เซนต์",
        decimal_pre: true,
        decimal_post: false,
    },
    Currency {
        pre: &["€", "EUR ", "EUR"],
        post: &["€", "EUR"],
        word: "ยูโร",
        minor: "เซนต์",
        decimal_pre: true,
        decimal_post: true,
    },
    Currency {
        pre: &["£", "GBP ", "GBP"],
        post: &["£", "GBP"],
        word: "ปอนด์",
        minor: "เพนซ์",
        decimal_pre: true,
        decimal_post: false,
    },
    Currency {
        pre: &["¥", "JPY ", "JPY"],
        post: &["¥", "円", "JPY"],
        word: "เยน",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₩", "KRW ", "KRW"],
        post: &["₩", "KRW"],
        word: "วอน",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₹", "Rs. ", "Rs.", "INR ", "INR"],
        post: &["₹", "INR"],
        word: "รูปี",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₽", "RUB ", "RUB"],
        post: &["₽", "RUB"],
        word: "รูเบิล",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₺", "TRY ", "TRY"],
        post: &["₺", "TRY"],
        word: "ลีรา",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₪", "ILS ", "ILS"],
        post: &["₪", "ILS"],
        word: "เชเกล",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₴", "UAH ", "UAH"],
        post: &["₴", "UAH"],
        word: "ฮริฟเนีย",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₫", "VND ", "VND"],
        post: &["₫", "VND"],
        word: "ดง",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["৳", "BDT ", "BDT"],
        post: &["৳", "BDT"],
        word: "ตากา",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₦", "NGN ", "NGN"],
        post: &["₦", "NGN"],
        word: "ไนรา",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["₱", "PHP ", "PHP"],
        post: &["₱", "PHP"],
        word: "เปโซ",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["֏", "AMD ", "AMD"],
        post: &["֏", "AMD"],
        word: "ดรัม",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["CHF ", "CHF"],
        post: &["CHF", "CHF "],
        word: "ฟรังก์สวิส",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["CA$", "CAD ", "CAD"],
        post: &["CAD", "CAD "],
        word: "ดอลลาร์แคนาดา",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["A$", "AUD ", "AUD"],
        post: &["AUD", "AUD "],
        word: "ดอลลาร์ออสเตรเลีย",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["R$", "BRL ", "BRL"],
        post: &["BRL", "BRL "],
        word: "เรียลบราซิล",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
    Currency {
        pre: &["MX$", "MXN ", "MXN"],
        post: &["MXN", "MXN "],
        word: "เปโซเม็กซิโก",
        minor: "",
        decimal_pre: false,
        decimal_post: false,
    },
];

pub(crate) fn thai_suffixes() -> impl Iterator<Item = &'static str> {
    CURRENCIES.iter().flat_map(|currency| currency.post.iter().copied())
}

struct MaskIndex {
    ascii: [u32; 128],
    other: Vec<(char, u32)>,
}
impl MaskIndex {
    fn new() -> Self {
        Self { ascii: [0; 128], other: Vec::new() }
    }
    fn add(&mut self, c: char, bit: u32) {
        if c.is_ascii() {
            self.ascii[c as usize] |= bit;
            return;
        }
        if let Some(row) = self.other.iter_mut().find(|row| row.0 == c) {
            row.1 |= bit;
        } else {
            self.other.push((c, bit));
        }
    }
    fn get(&self, c: char) -> u32 {
        if c.is_ascii() {
            return self.ascii[c as usize];
        }
        self.other.binary_search_by_key(&c, |row| row.0).ok().map_or(0, |i| self.other[i].1)
    }
}
struct CurrencyIndex {
    pre: MaskIndex,
    post: MaskIndex,
}
static INDEX: std::sync::LazyLock<CurrencyIndex> = std::sync::LazyLock::new(|| {
    let mut index = CurrencyIndex { pre: MaskIndex::new(), post: MaskIndex::new() };
    for (i, currency) in CURRENCIES.iter().enumerate() {
        for key in currency.pre {
            index.pre.add(key.chars().next().unwrap(), 1 << i);
        }
        for key in currency.post {
            index.post.add(key.chars().next_back().unwrap(), 1 << i);
        }
    }
    index.pre.other.sort_unstable_by_key(|row| row.0);
    index.post.other.sort_unstable_by_key(|row| row.0);
    index
});

fn masks(s: &str) -> (u32, u32) {
    let pre = s.chars().next().map_or(0, |c| INDEX.pre.get(c));
    let post = s.chars().next_back().map_or(0, |c| INDEX.post.get(c));
    (pre, post)
}

pub(crate) fn possible_start(c: char) -> bool {
    number::digit(c).is_some() || matches!(c, '-' | '−') || INDEX.pre.get(c) != 0
}

/// End alphabet of integer/decimal amounts, currency suffixes, shorthand,
/// and the source per-unit suffixes.
pub(crate) fn possible_end(c: char) -> bool {
    number::digit(c).is_some()
        || matches!(c, ',' | '¢' | 'k' | 'K' | 'm' | 'M' | 'h' | 'r' | 'g' | 'n' | 'y')
        || INDEX.post.get(c) != 0
}

fn plain_positive(s: &str) -> bool {
    let mut digits = s.chars();
    let Some(first) = digits.next() else { return false };
    if number::digit(first).is_none() || number::digit(first) == Some(0) {
        return false;
    }
    let mut count = 1;
    for c in digits {
        if number::digit(c).is_none() {
            return false;
        }
        count += 1;
    }
    count <= 18
}

/// This disjoint integer-only currency path cannot also be a range, decimal,
/// shorthand or per-unit path. It requires no number rendering to recognize.
fn simple_parts(s: &str) -> Option<(&str, &'static str)> {
    let (pre, post) = masks(s);
    let mut remaining = pre | post;
    while remaining != 0 {
        let i = remaining.trailing_zeros() as usize;
        let bit = 1u32 << i;
        remaining &= !bit;
        let currency = &CURRENCIES[i];
        if pre & bit != 0 {
            for &prefix in currency.pre {
                if let Some(n) = s.strip_prefix(prefix).filter(|n| plain_positive(n)) {
                    let word = if currency.word == "ดอลลาร์" && prefix != "$" {
                        "ดอลลาร์สหรัฐ"
                    } else {
                        currency.word
                    };
                    return Some((n, word));
                }
            }
        }
        if post & bit != 0 {
            for &suffix in currency.post {
                if let Some(n) = s.strip_suffix(suffix) {
                    let n = n.strip_suffix(' ').unwrap_or(n);
                    if plain_positive(n) {
                        let word = if currency.word == "ดอลลาร์" {
                            "ดอลลาร์สหรัฐ"
                        } else {
                            currency.word
                        };
                        return Some((n, word));
                    }
                }
            }
        }
    }
    let n = s.strip_suffix('¢')?;
    let n = n.strip_suffix(' ').unwrap_or(n);
    plain_positive(n).then_some((n, "เซนต์"))
}

pub(crate) fn simple(s: &str) -> bool {
    simple_parts(s).is_some()
}

pub(crate) fn render_simple(s: &str) -> Option<String> {
    let (n, word) = simple_parts(s)?;
    let mut spoken = number::positive(n)?;
    spoken.push_str(word);
    Some(spoken)
}

fn minor(s: &str, word: &str, zero_allowed: bool) -> Option<String> {
    if s == "00" && zero_allowed {
        return Some(String::new());
    }
    if let Some(rest) = s.strip_prefix('0') {
        if rest.chars().count() == 1 {
            return Some(format!("{}{word}", number::digit_words(rest)?));
        }
    }
    Some(format!("{}{word}", number::positive(s)?))
}
fn amount(s: &str, c: &Currency, pre: bool, key: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(n) = number::positive(s) {
        let word = if c.word == "ดอลลาร์" && (!pre || key != "$") {
            "ดอลลาร์สหรัฐ"
        } else {
            c.word
        };
        out.push(format!("{n}{word}"));
    }
    if (pre && c.decimal_pre) || (!pre && c.decimal_post) {
        for sep in if c.word == "ยูโร" { &['.', ','][..] } else { &['.'][..] } {
            if let Some((major, small)) = s.split_once(*sep) {
                if let (Some(n), Some(m)) = (number::positive(major), minor(small, c.minor, true)) {
                    out.push(format!("{n}{}{m}", c.word));
                }
                if major == "0" && small.chars().count() == 2 && matches!(c.word, "บาท" | "ดอลลาร์")
                {
                    if let Some(m) = minor(small, c.minor, false) {
                        out.push(if c.word == "บาท" {
                            m
                        } else {
                            format!("ศูนย์ดอลลาร์{m}")
                        });
                    }
                }
            }
        }
    }
    out
}
fn positive(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let (pre, post) = masks(s);
    let mut remaining = pre | post;
    while remaining != 0 {
        let i = remaining.trailing_zeros() as usize;
        let bit = 1u32 << i;
        remaining &= !bit;
        let c = &CURRENCIES[i];
        if pre & bit != 0 {
            for &p in c.pre {
                if let Some(rest) = s.strip_prefix(p) {
                    for x in amount(rest, c, true, p) {
                        out.push((x, 0));
                    }
                }
            }
        }
        if post & bit != 0 {
            for &p in c.post {
                if let Some(rest) = s.strip_suffix(p) {
                    let rest = rest.strip_suffix(' ').unwrap_or(rest);
                    for x in amount(rest, c, false, p) {
                        out.push((x, 0));
                    }
                }
            }
        }
    }
    if let Some(rest) = s.strip_suffix('¢') {
        let rest = rest.strip_suffix(' ').unwrap_or(rest);
        if let Some(n) = number::positive(rest) {
            out.push((format!("{n}เซนต์"), 0));
        }
    }
    for (p, w, allow_m) in [
        ("US$", "ดอลลาร์", true),
        ("USD", "ดอลลาร์", true),
        ("$", "ดอลลาร์", true),
        ("EUR", "ยูโร", true),
        ("€", "ยูโร", true),
        ("THB", "บาท", false),
        ("฿", "บาท", false),
    ] {
        if let Some(rest) = s.strip_prefix(p) {
            if let Some(n) = rest.strip_suffix('k').or_else(|| rest.strip_suffix('K')) {
                if n.chars().all(|c| number::digit(c).is_some()) {
                    if let Some(n) = number::positive(&format!("{n}000")) {
                        out.push((format!("{n}{w}"), -100_000));
                    }
                }
            }
            if allow_m {
                if let Some(n) = rest.strip_suffix('m').or_else(|| rest.strip_suffix('M')) {
                    if let Some(n) = decimal::positive(n) {
                        out.push((format!("{n}ล้าน{w}"), -100_000));
                    }
                }
            }
        }
    }
    out
}
fn splits(s: &str) -> impl Iterator<Item = (&str, &str)> {
    s.char_indices().filter_map(move |(i, c)| {
        if i > 0 && matches!(c, '-' | '–' | '—') {
            let a = &s[..i];
            let b = &s[i + c.len_utf8()..];
            Some((a.strip_suffix(' ').unwrap_or(a), b.strip_prefix(' ').unwrap_or(b)))
        } else {
            None
        }
    })
}
pub(crate) fn parse(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (x, c) in positive(s) {
        out.push((x, 1_080_000 + c));
    }
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for (x, c) in positive(rest) {
            out.push((format!("ลบ{x}"), 1_070_000 + c));
        }
    }
    for (suffix, w) in [
        ("/h", "ต่อชั่วโมง"),
        ("/hr", "ต่อชั่วโมง"),
        ("/kg", "ต่อกิโลกรัม"),
        ("/m", "ต่อเมตร"),
        ("/month", "ต่อเดือน"),
        ("/year", "ต่อปี"),
        ("/day", "ต่อวัน"),
    ] {
        if let Some(rest) = s.strip_suffix(suffix) {
            for (x, c) in positive(rest) {
                out.push((format!("{x}{w}"), 1_060_000 + c));
            }
        }
    }
    for (a, b) in splits(s) {
        for (p, w) in [("$", "ดอลลาร์"), ("฿", "บาท")] {
            if let (Some(a), Some(b)) = (a.strip_prefix(p), b.strip_prefix(p)) {
                if let (Some(a), Some(b)) = (number::positive(a), number::positive(b)) {
                    out.push((format!("{a}ถึง{b}{w}"), 1_050_000));
                }
            }
        }
        if let Some(b) = b.strip_suffix('€') {
            let b = b.strip_suffix(' ').unwrap_or(b);
            if let (Some(a), Some(b)) = (number::positive(a), number::positive(b)) {
                out.push((format!("{a}ถึง{b}ยูโร"), 1_050_000));
            }
        }
        for (x, cx) in positive(a) {
            for (y, cy) in positive(b) {
                out.push((format!("{x}ถึง{y}"), 1_050_000 + cx + cy));
            }
        }
    }
    out
}
