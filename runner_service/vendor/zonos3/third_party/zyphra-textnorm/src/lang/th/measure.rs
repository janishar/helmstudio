use super::{decimal, fraction, number};

const UNITS: &[(&str, &str)] = &[
    ("km", "กิโลเมตร"),
    ("กม.", "กิโลเมตร"),
    ("กม", "กิโลเมตร"),
    ("m", "เมตร"),
    ("ม.", "เมตร"),
    ("ม", "เมตร"),
    ("cm", "เซนติเมตร"),
    ("ซม.", "เซนติเมตร"),
    ("ซม", "เซนติเมตร"),
    ("mm", "มิลลิเมตร"),
    ("มม.", "มิลลิเมตร"),
    ("มม", "มิลลิเมตร"),
    ("µm", "ไมโครเมตร"),
    ("μm", "ไมโครเมตร"),
    ("mi", "ไมล์"),
    ("kg", "กิโลกรัม"),
    ("กก.", "กิโลกรัม"),
    ("กก", "กิโลกรัม"),
    ("g", "กรัม"),
    ("mg", "มิลลิกรัม"),
    ("µg", "ไมโครกรัม"),
    ("μg", "ไมโครกรัม"),
    ("L", "ลิตร"),
    ("l", "ลิตร"),
    ("mL", "มิลลิลิตร"),
    ("ml", "มิลลิลิตร"),
    ("s", "วินาที"),
    ("sec", "วินาที"),
    ("ms", "มิลลิวินาที"),
    ("min", "นาที"),
    ("h", "ชั่วโมง"),
    ("hr", "ชั่วโมง"),
    ("ชม.", "ชั่วโมง"),
    ("ชม", "ชั่วโมง"),
    ("km/h", "กิโลเมตรต่อชั่วโมง"),
    ("km/hr", "กิโลเมตรต่อชั่วโมง"),
    ("m/s", "เมตรต่อวินาที"),
    ("mph", "ไมล์ต่อชั่วโมง"),
    ("m²", "ตารางเมตร"),
    ("m2", "ตารางเมตร"),
    ("cm²", "ตารางเซนติเมตร"),
    ("cm2", "ตารางเซนติเมตร"),
    ("km²", "ตารางกิโลเมตร"),
    ("km2", "ตารางกิโลเมตร"),
    ("m³", "ลูกบาศก์เมตร"),
    ("m3", "ลูกบาศก์เมตร"),
    ("cm³", "ลูกบาศก์เซนติเมตร"),
    ("cm3", "ลูกบาศก์เซนติเมตร"),
    ("°C", "องศาเซลเซียส"),
    ("℃", "องศาเซลเซียส"),
    ("°F", "องศาฟาเรนไฮต์"),
    ("K", "เคลวิน"),
    ("Gbps", "กิกะบิตต่อวินาที"),
    ("Mbps", "เมกะบิตต่อวินาที"),
    ("kbps", "กิโลบิตต่อวินาที"),
    ("mAh", "มิลลิแอมแปร์ชั่วโมง"),
    ("kWh", "กิโลวัตต์ชั่วโมง"),
    ("hPa", "เฮกโตปาสกาล"),
    ("kcal", "กิโลแคลอรี"),
    ("dB", "เดซิเบล"),
    ("Ω", "โอห์ม"),
    ("Ω", "โอห์ม"),
    ("Hz", "เฮิรตซ์"),
    ("kHz", "กิโลเฮิรตซ์"),
    ("MHz", "เมกะเฮิรตซ์"),
    ("GHz", "กิกะเฮิรตซ์"),
    ("W", "วัตต์"),
    ("kW", "กิโลวัตต์"),
    ("MW", "เมกะวัตต์"),
    ("V", "โวลต์"),
    ("mV", "มิลลิโวลต์"),
    ("A", "แอมแปร์"),
    ("mA", "มิลลิแอมแปร์"),
    ("KB", "กิโลไบต์"),
    ("kB", "กิโลไบต์"),
    ("MB", "เมกะไบต์"),
    ("GB", "กิกะไบต์"),
    ("TB", "เทระไบต์"),
    ("%", "เปอร์เซ็นต์"),
    ("‰", "เปอร์มิล"),
];

pub(crate) fn thai_suffixes() -> impl Iterator<Item = &'static str> {
    UNITS.iter().map(|&(key, _)| key)
}

type UnitIndex = Vec<(char, &'static str, &'static str)>;
static UNIT_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| {
    let mut index: UnitIndex =
        UNITS.iter().map(|&(key, value)| (key.chars().next_back().unwrap(), key, value)).collect();
    // Stable ordering retains the previous longest-key tie behavior.
    index.sort_by_key(|row| row.0);
    index
});

pub(super) fn possible_end(c: char) -> bool {
    UNIT_INDEX.binary_search_by_key(&c, |row| row.0).is_ok()
}

fn num(s: &str) -> Option<(String, i64)> {
    decimal::decimal(s).map(|x| (x, 1_000_000)).or_else(|| number::number(s).map(|x| (x, 1_100_000)))
}
fn unit(s: &str) -> Option<(&str, &'static str)> {
    let last = s.chars().next_back()?;
    let i = UNIT_INDEX.partition_point(|row| row.0 < last);
    UNIT_INDEX[i..]
        .iter()
        .take_while(|row| row.0 == last)
        .filter_map(|&(_, u, w)| s.strip_suffix(u).map(|v| (v, w, u.len())))
        .max_by_key(|x| x.2)
        .map(|(v, w, _)| (v.strip_suffix(' ').unwrap_or(v), w))
}
fn single(s: &str) -> Option<(String, i64)> {
    let (n, u) = unit(s)?;
    let (mut n, c) = num(n)?;
    n.push_str(u);
    Some((n, c))
}

/// Recognize an integer measure without spelling it and distinguish an absent
/// unit from a non-integer unit reading requiring the general parser.
pub(crate) enum UnitMatch {
    Absent,
    Integer,
    Other,
}
pub(crate) fn recognize_unit(s: &str) -> UnitMatch {
    match unit(s) {
        None => UnitMatch::Absent,
        Some((n, _)) if number::valid_number(n) => UnitMatch::Integer,
        Some(_) => UnitMatch::Other,
    }
}

pub(crate) fn render_simple(s: &str) -> Option<String> {
    single(s).map(|(v, _)| v)
}
fn splits(s: &str) -> impl Iterator<Item = (&str, &str)> {
    s.char_indices().filter_map(move |(i, c)| {
        if matches!(c, '-' | '–' | '—' | '~' | '〜' | '～') && i > 0 {
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
    if let Some((n, u)) = unit(s) {
        if let Some((x, c)) = num(n) {
            out.push((format!("{x}{u}"), 1_060_000 + c));
        }
        if let Some(x) = fraction::fraction(n) {
            out.push((format!("{x}{u}"), 1_050_000));
        }
    }
    if let Some((prefix, rest)) =
        s.strip_prefix('~').map(|s| ("ประมาณ", s)).or_else(|| s.strip_prefix('±').map(|s| ("บวกลบ", s)))
    {
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        if let Some((x, c)) = single(rest).or_else(|| num(rest)) {
            out.push((format!("{prefix}{x}"), 1_040_000 + c));
        }
    }
    for (a, b) in splits(s) {
        if let Some((bn, u)) = unit(b) {
            if let (Some((a, ca)), Some((b, cb))) = (num(a), num(bn)) {
                out.push((format!("{a}ถึง{b}{u}"), -1_500_000 + ca + cb));
            }
        }
        if let (Some((a, ca)), Some((b, cb))) = (single(a), single(b)) {
            out.push((format!("{a}ถึง{b}"), 1_030_000 + ca + cb));
        }
    }
    out
}
