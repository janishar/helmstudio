//! Source's bounded Roman numerals are only accepted in contextual phrases.
use super::cardinal::base_num;
const ROMANS: [&str; 21] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI",
];
const MONARCH: [&str; 16] = [
    "הראשון",
    "השני",
    "השלישי",
    "הרביעי",
    "החמישי",
    "השישי",
    "השביעי",
    "השמיני",
    "התשיעי",
    "העשירי",
    "האחד עשר",
    "השנים עשר",
    "השלושה עשר",
    "הארבעה עשר",
    "החמישה עשר",
    "השישה עשר",
];
const CENTURY: [&str; 21] = [
    "הראשונה",
    "השנייה",
    "השלישית",
    "הרביעית",
    "החמישית",
    "השישית",
    "השביעית",
    "השמינית",
    "התשיעית",
    "העשירית",
    "האחת עשרה",
    "השתים עשרה",
    "השלוש עשרה",
    "הארבע עשרה",
    "החמש עשרה",
    "השש עשרה",
    "השבע עשרה",
    "השמונה עשרה",
    "התשע עשרה",
    "העשרים",
    "העשרים ואחת",
];
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (i, r) in ROMANS.iter().enumerate() {
        let Some(lead) = s.strip_suffix(r) else { continue };
        if let Some(key) = lead.strip_suffix(' ') {
            if [
                "פרק", "חלק", "כרך", "סעיף", "לוח", "נספח", "שער", "Chapter", "Part", "Section", "Volume", "Book",
                "מאה",
            ]
            .contains(&key)
            {
                if let Some(n) = base_num(&(i + 1).to_string(), false) {
                    out.push((format!("{key} {n}"), 900_000));
                }
            }
            if [
                "לואי",
                "הנרי",
                "שלמה",
                "דוד",
                "ג'ורג'",
                "אליזבת",
                "ויליאם",
                "פיליפ",
                "קרל",
                "ניקולאי",
                "אלכסנדר",
                "נפוליאון",
                "מלך",
                "המלך",
                "האפיפיור",
                "קיסר",
                "הקיסר",
                "Louis",
                "Henry",
                "George",
                "Elizabeth",
                "William",
                "King",
                "Pope",
            ]
            .contains(&key)
            {
                let n = if i < 16 {
                    Some(MONARCH[i])
                } else if i == 20 {
                    Some("העשרים ואחד")
                } else {
                    None
                };
                if let Some(n) = n {
                    out.push((format!("{key} {n}"), 900_000));
                }
            }
        }
        for (prefix, spoken) in [
            ("המאה ה-", "המאה"),
            ("המאה ה־", "המאה"),
            ("המאה", "המאה"),
            ("במאה ה-", "במאה"),
            ("במאה ה־", "במאה"),
            ("במאה", "במאה"),
        ] {
            if lead == prefix || lead.strip_suffix(' ') == Some(prefix) {
                out.push((format!("{spoken} {}", CENTURY[i]), 900_000));
            }
        }
    }
    out
}
