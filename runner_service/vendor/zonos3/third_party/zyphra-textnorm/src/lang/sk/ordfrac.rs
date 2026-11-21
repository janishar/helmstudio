use super::number::positive;
pub(super) fn ordinal(s: &str) -> Option<String> {
    let direct = match s {
        "6-ty" => Some("šiesty"),
        "2-hý" => Some("druhý"),
        "2-há" => Some("druhá"),
        "2-hé" => Some("druhé"),
        "2-ho" => Some("druhého"),
        "1-vý" => Some("prvý"),
        "1-vá" => Some("prvá"),
        "1-vé" => Some("prvé"),
        "3-tí" => Some("tretí"),
        "3-tia" => Some("tretia"),
        "3-tie" => Some("tretie"),
        "4-tý" => Some("štvrtý"),
        "5-ty" => Some("piaty"),
        "1950-te" => Some("tisíc deväťsto päťdesiate"),
        "20-te" => Some("dvadsiate"),
        "84-om" | "84-tom" => Some("osemdesiatom štvrtom"),
        _ => None,
    };
    if let Some(x) = direct {
        return Some(x.into());
    }
    let s = s.strip_suffix('.')?;
    if s == "0" {
        return Some("nultý".into());
    }
    if s.starts_with('0') || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    let ones = ["", "prvý", "druhý", "tretí", "štvrtý", "piaty", "šiesty", "siedmy", "ôsmy", "deviaty"];
    let teens = [
        "desiaty",
        "jedenásty",
        "dvanásty",
        "trinásty",
        "štrnásty",
        "pätnásty",
        "šestnásť",
        "sedemnásty",
        "osemnásty",
        "devätnásty",
    ];
    let tens = [
        "",
        "",
        "dvadsiaty",
        "tridsiaty",
        "štyridsiaty",
        "päťdesiaty",
        "šesťdesiaty",
        "sedemdesiaty",
        "osemdesiaty",
        "deväťdesiaty",
    ];
    let hundreds = ["", "stý", "dvestý", "tristý", "štyristý", "päťstý", "šesťstý", "sedemstý", "osemstý", "deväťstý"];
    match n {
        1..=9 => Some(ones[n].into()),
        10..=19 => Some(teens[n - 10].into()),
        20..=99 => Some(if n % 10 == 0 { tens[n / 10].into() } else { spoken!("{} {}", tens[n / 10], ones[n % 10]) }),
        100..=900 if n % 100 == 0 => Some(hundreds[n / 100].into()),
        1000 => Some("tisíci".into()),
        _ => None,
    }
}
pub(super) fn fraction(s: &str) -> Option<String> {
    let (neg, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", s) } else { ("", s) };
    let entries = [
        ("½", "jedna polovica"),
        ("⅓", "jedna tretina"),
        ("⅔", "dve tretiny"),
        ("¼", "jedna štvrtina"),
        ("¾", "tri štvrtiny"),
        ("⅕", "jedna pätina"),
        ("⅖", "dve pätiny"),
        ("⅗", "tri pätiny"),
        ("⅘", "štyri pätiny"),
        ("⅙", "jedna šestina"),
        ("⅚", "päť šestín"),
        ("⅐", "jedna sedmina"),
        ("⅛", "jedna osmina"),
        ("⅜", "tri osminy"),
        ("⅝", "päť osmín"),
        ("⅞", "sedem osmín"),
        ("⅑", "jedna devätina"),
        ("⅒", "jedna desatina"),
        ("1/2", "jedna polovica"),
        ("3/4", "tri štvrtiny"),
        ("2/3", "dve tretiny"),
        ("5/7", "päť sedmín"),
        ("11/12", "jedenásť dvanástin"),
        ("21/100", "dvadsaťjeden stotín"),
        ("22/7", "dvadsaťdva sedmín"),
        ("125/1000", "stodvadsaťpäť tisícin"),
        ("101/250", "stojeden dvestopäťdesiatin"),
        ("1/3", "jedna tretina"),
        ("1/4", "jedna štvrtina"),
        ("1/5", "jedna pätina"),
        ("2/5", "dve pätiny"),
        ("3/5", "tri pätiny"),
        ("4/5", "štyri pätiny"),
        ("1/6", "jedna šestina"),
        ("5/6", "päť šestín"),
        ("1/7", "jedna sedmina"),
        ("1/8", "jedna osmina"),
        ("3/8", "tri osminy"),
        ("5/8", "päť osmín"),
        ("7/8", "sedem osmín"),
        ("1/9", "jedna devätina"),
        ("1/10", "jedna desatina"),
    ];
    for (src, dst) in entries {
        if s == src {
            return Some(spoken!("{neg}{dst}"));
        }
        if let Some(p) = s.strip_suffix(src) {
            let vulgar = !src.contains('/');
            let p = if vulgar { p.strip_suffix(' ').unwrap_or(p) } else { p.strip_suffix(' ')? };
            let n: usize = p.parse().ok()?;
            if n == 0 || n > 999 || p.starts_with('0') {
                continue;
            }
            let whole = match n {
                1 => "jedna celá".into(),
                2 => "dve celé".into(),
                3 => "tri celé".into(),
                4 => "štyri celé".into(),
                _ => spoken!("{} celých", positive(p)?),
            };
            return Some(spoken!("{neg}{whole} {dst}"));
        }
    }
    None
}
