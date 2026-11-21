//! Bounded dot and definite ordinal forms.
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let dots = ["ראשון", "שני", "שלישי", "רביעי", "חמישי", "שישי", "שביעי", "שמיני", "תשיעי", "עשירי"];
    if let Some(n) = s.strip_suffix('.') {
        if let Ok(i) = n.parse::<usize>() {
            if (1..=10).contains(&i) && n == i.to_string() {
                return vec![(dots[i - 1].into(), 1_020_000)];
            }
        }
    }
    if let Some(n) = s.strip_prefix("ה-") {
        let v = match n {
            "1" => Some("הראשון"),
            "2" => Some("השני"),
            "3" => Some("השלישי"),
            "4" => Some("הרביעי"),
            "5" => Some("החמישי"),
            "6" => Some("השישי"),
            "7" => Some("השביעי"),
            "8" => Some("השמיני"),
            "9" => Some("התשיעי"),
            "10" => Some("העשירי"),
            "11" => Some("האחד עשר"),
            "12" => Some("השנים עשר"),
            "13" => Some("השלושה עשר"),
            "20" => Some("העשרים"),
            "21" => Some("העשרים ואחד"),
            "30" => Some("השלושים"),
            "50" => Some("החמישים"),
            "100" => Some("המאה"),
            _ => None,
        };
        if let Some(v) = v {
            return vec![(v.into(), 1_020_000)];
        }
    }
    Vec::new()
}
