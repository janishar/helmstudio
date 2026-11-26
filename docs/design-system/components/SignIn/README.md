# SignIn

The Sign in screen ((auth)/layout.tsx and auth-form.tsx): the card in the middle, takes floating behind it, the theme select top right.

- Page `hs-auth`: the full window, 24px padding, the card centred.
- Theme: a pill 20px from the top and 24px from the right on `s1` (6/6/6/14px), "Theme" as a `label` and a 32px `s3` pill select.
- Floating takes (from 768px wide): six showcase pictures on `s3`, 28 / 12 corners, turned −8° to 10°, an inner light and a deep drop, each with a `mono` 10px white pill tag on a 45% `shd` wash ("Qwen Image 2.1", "▶ LTX-2.5", "take"). Decoration only, hidden from readers.
- Card `hs-auth-card`: 400px, 32/32/28px padding, 36 / 14 corners, `s1` with a top sheen and a deep drop; parts 16px apart. The Logo tile (44px) and "helmstudio" in display 26px 700 at −0.02em; an error line (13px `bad` on `s2`, 12px corners) only after a failed try; Email and Password as `label` over 44px `.in` fields; Sign in as the `key` at 50px, 15px ("Signing in…" while it works); "No account? Ask your admin." 13px `t2` with an Info.
- Its buttons carry the drawer's bevel shadow.

There is no public sign-up: the admin makes every account. The preview's fields are inert stand-ins and its pictures neutral fills.
