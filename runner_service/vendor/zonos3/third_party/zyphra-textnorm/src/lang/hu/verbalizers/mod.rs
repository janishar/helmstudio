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

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod telephone;
pub mod time;

use crate::tag::{Dict, Field, Value};

#[inline]
pub fn s<'a>(d: &'a [Field], i: usize, key: &str) -> Option<&'a str> {
    match d.get(i)? {
        (k, Value::Str(v)) if k == key => Some(v.as_ref()),
        _ => None,
    }
}

#[inline]
pub fn is_key(d: &[Field], i: usize, key: &str) -> bool {
    d.get(i).is_some_and(|(k, _)| k == key)
}

#[inline]
pub fn dict<'t, 'a>(d: &'t [Field<'a>], i: usize, key: &str) -> Option<&'t Dict<'a>> {
    match d.get(i)? {
        (k, Value::Dict(v)) if k == key => Some(v),
        _ => None,
    }
}

pub fn find_str<'a>(d: &'a [Field], key: &str) -> Option<&'a str> {
    for (k, v) in d {
        if k == key {
            if let Value::Str(s) = v {
                return Some(s.as_ref());
            }
        }
    }
    None
}

pub fn find_dict<'t, 'a>(d: &'t [Field<'a>], key: &str) -> Option<&'t Dict<'a>> {
    for (k, v) in d {
        if k == key {
            if let Value::Dict(sub) = v {
                return Some(sub);
            }
        }
    }
    None
}

pub fn rest_is_po(d: &[Field], i: usize) -> bool {
    d[i.min(d.len())..].iter().all(|(k, v)| k == "preserve_order" && *v == Value::True)
}
