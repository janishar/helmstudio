const R: [&str; 21] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI",
];
const M: [&str; 21] = [
    "prvý",
    "druhý",
    "tretí",
    "štvrtý",
    "piaty",
    "šiesty",
    "siedmy",
    "ôsmy",
    "deviaty",
    "desiaty",
    "jedenásty",
    "dvanásty",
    "trinásty",
    "štrnásty",
    "pätnásty",
    "šestnásty",
    "sedemnásty",
    "osemnásty",
    "devätnásty",
    "dvadsiaty",
    "dvadsiaty prvý",
];
const F: [&str; 21] = [
    "prvá",
    "druhá",
    "tretia",
    "štvrtá",
    "piata",
    "šiesta",
    "siedma",
    "ôsma",
    "deviata",
    "desiata",
    "jedenásta",
    "dvanásta",
    "trinásta",
    "štrnásta",
    "pätnásta",
    "šestnásta",
    "sedemnásta",
    "osemnásta",
    "devätnásta",
    "dvadsiata",
    "dvadsiata prvá",
];
const N: [&str; 21] = [
    "prvé",
    "druhé",
    "tretie",
    "štvrté",
    "piate",
    "šieste",
    "siedme",
    "ôsme",
    "deviate",
    "desiate",
    "jedenáste",
    "dvanáste",
    "trináste",
    "štrnáste",
    "pätnáste",
    "šestnáste",
    "sedemnáste",
    "osemnáste",
    "devätnáste",
    "dvadsiate",
    "dvadsiate prvé",
];
pub(super) fn roman(s: &str) -> Option<String> {
    match s {
        "XL" => return Some("X L".into()),
        "MIX" => return Some("M I X".into()),
        "CD" => return Some("C D".into()),
        "DJ" => return Some("D J".into()),
        _ => {}
    }
    if let Some((prefix, written)) = s.rsplit_once(' ') {
        let number = written.strip_suffix('.').unwrap_or(written);
        if let Some(i) = R.iter().position(|r| *r == number) {
            let fem = ["Kapitola", "kapitola", "Hlava", "hlava", "Časť", "časť"].contains(&prefix);
            let masc = [
                "Diel",
                "diel",
                "Oddiel",
                "oddiel",
                "Článok",
                "článok",
                "Zväzok",
                "zväzok",
                "Karol",
                "Ján Pavol",
                "Ľudovít",
                "František",
                "Alžbeta",
                "Henrich",
                "Juraj",
                "Alexander",
                "Mikuláš",
                "Peter",
                "Viliam",
                "Ferdinand",
                "Štefan",
                "Václav",
                "Matej",
                "Žigmund",
            ]
            .contains(&prefix);
            if fem || masc {
                return Some(spoken!("{prefix} {}", if fem { F[i] } else { M[i] }));
            }
        }
    }
    if let Some((written, tail)) = s.split_once(' ') {
        let number = written.strip_suffix('.').unwrap_or(written);
        if ["storočie", "storočia", "storočí", "st.", "stor."].contains(&tail) {
            if let Some(i) = R.iter().position(|r| *r == number) {
                return Some(spoken!("{} {tail}", N[i]));
            }
        }
    }
    None
}
