// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use super::{is_key, s};
use crate::tag::Field;

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let mut out = String::new();
    if is_key(d, i, "negative") {
        if s(d, i, "negative")? != "true" {
            return None;
        }
        out.push_str("mínusz ");
        i += 1;
    }
    let val = s(d, i, "integer")?;
    out.push_str(val);
    i += 1;
    if i != d.len() {
        return None;
    }
    Some(out)
}
