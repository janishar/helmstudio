// The cats (plan 30, docs/plan/30-cats-easter-egg.md §34; board docs/mock/CatsEasterEgg.dc.html): a double-click on
// the rail logo plays one of four short scenes in a strip at the bottom of the content area. Its own chunk, loaded
// with import() on the first double-click. The rig, poses, walk and scenes are the board's, scoped under .cats.
// Positions run on the Web Animations API (transform and opacity only); poses are classes set at the scene's times.

// .rm (the reduced-motion pair) keeps its 2 s fade with !important: globals.css cuts every animation to 0.01ms there
const CSS = `.cats{position:fixed;inset:auto 0 0 90px;width:auto;height:130px;margin:0;border:0;padding:0;background:none;overflow:hidden;pointer-events:none;z-index:2147483647}
.cats .kit{left:0;bottom:10px}.cats .xtra{position:absolute;bottom:0;left:0;will-change:transform,opacity}
.cats .rm{position:absolute;right:24px;bottom:8px;width:194px;height:100px;animation:c-fade 2s both;animation-duration:2s!important}
.cats .rm .kit{bottom:0;transform:scale(.8);transform-origin:50% 100%}
@keyframes c-fade{0%,100%{opacity:0}15%,85%{opacity:1}}
.cats .kw{--fur:#faf7f1;--fur2:#e2dbce;--crease:#e8e0d2;--muz:#fffdf9;--inear:#f2b5b0;--eye:#5b9bd0;--pup:#22303e;--nose:#ec8f98;--ink:#3a3228;--wh:#c9c0b0;--edge:transparent}
.cats .kb{--fur:#1d1c22;--fur2:#101014;--crease:#2e2c35;--muz:#28262e;--inear:#4d3e48;--eye:#e8c95a;--pup:#1a1410;--nose:#7d6170;--ink:#9a93a3;--wh:#6d6874;--edge:color-mix(in srgb,var(--t2) 50%,transparent)}
[data-theme="parchment"] .cats .kw{--edge:var(--dash)}
[data-theme="parchment"] .cats .kb{--edge:transparent}
.cats .kit{position:absolute;width:120px;height:96px;will-change:transform}
.cats .kit.kb .fx{transform:scale(1.07);transform-origin:50% 100%}
.cats .kit .fx{width:120px;height:96px;filter:drop-shadow(1px 0 0 var(--edge)) drop-shadow(-1px 0 0 var(--edge)) drop-shadow(0 1px 0 var(--edge)) drop-shadow(0 -1px 0 var(--edge))}
.cats .kit svg{display:block;overflow:visible;transition:transform .18s ease}
.cats .kit[data-face="l"] svg{transform:scaleX(-1)}
.cats .gs{position:absolute;left:18px;bottom:-3px;width:84px;height:8px;border-radius:50%;background:color-mix(in srgb,var(--shd) 30%,transparent);filter:blur(1.5px)}
[data-theme="parchment"] .cats .gs{background:color-mix(in srgb,var(--shd) 55%,transparent)}
.cats .kit svg g,.cats .kit svg path{transition:transform .35s ease,opacity .16s linear var(--od,0s)}
.cats .p-head{transform-origin:90px 46px}.cats .p-body{transform-origin:30px 72px}.cats .p-tail{transform-origin:21px 50px}.cats .t2{transform-origin:10.8px 38.6px}.cats .t3{transform-origin:14.8px 23.6px}
.cats .lb{transform-origin:40px 58px}.cats .lb .kn{transform-origin:39px 75px}.cats .lf{transform-origin:82px 58px}.cats .lf .kn{transform-origin:82.7px 75px}
.cats .eyes-o{transform-origin:97px 32.6px}
.cats .b-stand{transform-origin:24px 74px}.cats .b-sit{transform-origin:30px 90px;transform:rotate(16deg) scaleY(.8)}.cats .b-loaf{transform-origin:52px 90px;transform:scale(1.06,1.3)}
.cats .tl-curl{transform-origin:20px 88px;transform:rotate(-12deg) scaleX(.7)}
.cats .b-sit,.cats .b-loaf,.cats .tl-curl,.cats .eyes-c{opacity:0;--od:.18s}
@keyframes c-blink{0%,90%,96%,100%{transform:none}93%{transform:scaleY(.1)}}
@keyframes c-ts1{from{transform:rotate(-6deg)}to{transform:rotate(8deg)}}
@keyframes c-ts2{from{transform:rotate(-11deg)}to{transform:rotate(11deg)}}
@keyframes c-ts3{from{transform:rotate(-18deg)}to{transform:rotate(18deg)}}
.cats .eyes-o{animation:c-blink 3.8s ease-in-out infinite}.cats .kb .eyes-o{animation-delay:-1.9s}
.cats .p-tail{animation:c-ts1 2.4s ease-in-out infinite alternate}
.cats .t2{animation:c-ts2 2.4s ease-in-out calc(var(--ph,0s) - .5s) infinite alternate}
.cats .t3{animation:c-ts3 2.4s ease-in-out calc(var(--ph,0s) - 1s) infinite alternate}
@keyframes c-wth{0%{transform:rotate(-16deg)}55%{transform:rotate(16deg)}78%{transform:rotate(3deg)}100%{transform:rotate(-16deg)}}
@keyframes c-wtk{0%,55%{transform:none}76%{transform:rotate(30deg)}100%{transform:none}}
@keyframes c-wfu{0%{transform:rotate(-15deg)}55%{transform:rotate(15deg)}78%{transform:rotate(-2deg)}100%{transform:rotate(-15deg)}}
@keyframes c-wfk{0%,55%{transform:none}76%{transform:rotate(40deg)}100%{transform:none}}
@keyframes c-wbob{0%,50%,100%{transform:translateY(.4px)}25%,75%{transform:translateY(-1.4px)}}
@keyframes c-whd{0%,50%,100%{transform:translateY(.8px) rotate(.8deg)}25%,75%{transform:translateY(-1.2px) rotate(-1.2deg)}}
.cats .p-lbf,.cats .p-lfn{--ld:-.42s}
.cats :is(.walk,.run){--wd:.84s}.cats .run{--wd:.5s}
.cats :is(.walk,.run) .lb{animation:c-wth var(--wd) ease-in-out calc(var(--ph,0s) + var(--ld,0s)) infinite}
.cats :is(.walk,.run) .lb .kn{animation:c-wtk var(--wd) ease-in-out calc(var(--ph,0s) + var(--ld,0s)) infinite}
.cats :is(.walk,.run) .lf{animation:c-wfu var(--wd) ease-in-out calc(var(--ph,0s) + var(--ld,0s)) infinite}
.cats :is(.walk,.run) .lf .kn{animation:c-wfk var(--wd) ease-in-out calc(var(--ph,0s) + var(--ld,0s)) infinite}
.cats :is(.walk,.run) .p-body{animation:c-wbob var(--wd) ease-in-out var(--ph,0s) infinite}
.cats :is(.walk,.run) .p-head{animation:c-whd var(--wd) ease-in-out calc(var(--ph,0s) - .78s) infinite}
.cats .walk .p-tail{animation-duration:1.68s;animation-delay:var(--ph,0s)}
.cats .walk :is(.t2,.t3){animation-duration:1.68s}
.cats .run .p-tail{animation:none;transform:rotate(-40deg)}.cats .run :is(.t2,.t3){animation-duration:.5s}
.cats .sit .b-stand,.cats .wave .b-stand{transform:rotate(-22deg);opacity:0;--od:.18s}
.cats .sit .b-sit,.cats .wave .b-sit{transform:none;opacity:1;--od:0s}
.cats .sit .lb,.cats .wave .lb{opacity:0;--od:.18s;transform:translateY(6px) rotate(-30deg)}
.cats .sit :is(.p-tail,.t2,.t3),.cats .wave :is(.p-tail,.t2,.t3){animation:none}
.cats .sit .p-tail,.cats .wave .p-tail{opacity:0;--od:.18s;transform:rotate(-50deg)}
.cats .sit .tl-curl,.cats .wave .tl-curl{opacity:1;--od:0s;transform:none}
.cats .sit .p-head,.cats .wave .p-head{transform:translate(-4px,-6px)}
.cats .sit .lf,.cats .wave .lf{transform:translateX(1px)}
.cats .wave .p-lfn{transform:translate(1px,-2px) rotate(-100deg)}.cats .wave .p-lfn .kn{transform:rotate(-40deg)}
.cats .wave .p-head{transform:translate(-4px,-6px) rotate(-5deg)}
.cats .stretch .p-body{transform:rotate(13deg)}
.cats .stretch .p-head{transform:translate(3px,17px) rotate(10deg)}
.cats .stretch .lf{transform:translate(-1px,12px) rotate(-66deg)}.cats .stretch .lf .kn{transform:rotate(-14deg)}
.cats .stretch .p-tail{animation:none;transform:rotate(10deg)}
.cats .stretch .eyes-o,.cats .boop .eyes-o,.cats .curl .eyes-o,.cats .wave .eyes-o{opacity:0;--od:.18s;animation:none}
.cats .stretch .eyes-c,.cats .boop .eyes-c,.cats .curl .eyes-c,.cats .wave .eyes-c{opacity:1;--od:0s}
@keyframes c-breathe{from{transform:none}to{transform:scale(1.015,1.05)}}
.cats .curl .b-stand{transform:translateY(10px) scale(.95,.7);opacity:0;--od:.18s}
.cats .curl .b-loaf{transform:none;opacity:1;--od:0s;animation:c-breathe 1.4s ease-in-out infinite alternate}
.cats .curl :is(.lb,.lf){transform:translateY(8px) rotate(-40deg);opacity:0;--od:.12s}
.cats .curl :is(.p-tail,.t2,.t3){animation:none}.cats .curl .p-tail{opacity:0;--od:.18s;transform:rotate(-60deg)}
.cats .curl .tl-curl{opacity:1;--od:0s;transform:none}
.cats .curl .p-head{transform:translate(-8px,34px) rotate(-9deg)}
.cats .boop .p-head{transform:translate(5px,1px) rotate(6deg)}
.cats .boop .p-tail{animation:none;transform:rotate(12deg)}
@keyframes c-swat{from{transform:translateY(-2px) rotate(-86deg)}to{transform:translateY(2px) rotate(-40deg)}}
@keyframes c-flick{from{transform:rotate(-24deg)}to{transform:rotate(28deg)}}
.cats .bat .p-body{transform:translateY(3px) rotate(-5deg)}.cats .bat .p-head{transform:translate(0,4px) rotate(-4deg)}
.cats .bat .lb{transform:translateY(2px) rotate(-6deg)}.cats .bat .lb .kn{transform:rotate(10deg)}
.cats .bat .p-lfn{animation:c-swat .32s ease-in-out infinite alternate}.cats .bat .p-lfn .kn{transform:rotate(-32deg)}
.cats .bat .p-tail,.cats .crouch .p-tail{animation:none;transform:rotate(-58deg)}
.cats .bat .t3,.cats .crouch .t3{animation:c-flick .2s ease-in-out infinite alternate}
.cats .crouch .p-body{transform:translateY(9px) rotate(3deg)}.cats .crouch .p-head{transform:translate(3px,12px) rotate(4deg)}
.cats .crouch .lb{transform:translateY(4px) rotate(-30deg)}.cats .crouch .lb .kn{transform:rotate(44deg)}
.cats .crouch .lf{transform:translate(0,6px) rotate(20deg)}.cats .crouch .lf .kn{transform:rotate(-92deg)}
.cats .still *{animation-play-state:paused!important}`;

const PUP_HE = `<ellipse cx="90.5" cy="32.8" rx=".9" ry="2.7" fill="var(--pup)"/><ellipse cx="103.7" cy="32.8" rx=".9" ry="2.7" fill="var(--pup)"/>`;
const PUP_SHE = `<circle cx="90.6" cy="33" r="1.6" fill="var(--pup)"/><circle cx="103.8" cy="33" r="1.6" fill="var(--pup)"/><path d="M106.3 30.1l2-1.5M87.7 30.1l-1.9-1.4" stroke="var(--ink)" stroke-width=".9" stroke-linecap="round"/>`;
const CHEEKS = `<ellipse cx="86.4" cy="38.8" rx="2.6" ry="2" fill="#f2a3a8" opacity=".55"/><ellipse cx="107.6" cy="38.8" rx="2.6" ry="2" fill="#f2a3a8" opacity=".55"/>`;
const COLLAR_HE = `<path d="M83.4 45.8Q97 53.4 110.6 45.2" fill="none" stroke="color-mix(in srgb,var(--a1) 55%,#000)" stroke-width="3" stroke-linecap="round"/><circle cx="97" cy="54" r="3.7" fill="var(--a1)"/><path d="M95.4 55.2h3.2" stroke="color-mix(in srgb,var(--a1) 45%,#000)" stroke-width="1" stroke-linecap="round"/><circle cx="96" cy="52.8" r=".9" fill="#fff" opacity=".7"/>`;
const COLLAR_SHE = `<path d="M83.4 45.8Q97 53.4 110.6 45.2" fill="none" stroke="var(--a2)" stroke-width="3.4" stroke-linecap="round"/><path d="M97 56.4c-2.6-1.6-3.4-2.8-3.4-3.8a1.7 1.7 0 0 1 3.4-.4a1.7 1.7 0 0 1 3.4.4c0 1-.8 2.2-3.4 3.8z" fill="var(--a2)"/>`;
const rig = (he: boolean) =>
  `<svg viewBox="0 0 120 96" width="120" height="96" aria-hidden="true"><g class="p-tail"><path d="M21 50C15.5 49.5 11.6 45 10.8 38.6" fill="none" stroke="var(--fur)" stroke-width="6.6" stroke-linecap="round"/><g class="t2"><path d="M10.8 38.6C10.2 32.6 11.4 27.4 14.8 23.6" fill="none" stroke="var(--fur)" stroke-width="5.4" stroke-linecap="round"/><g class="t3"><path d="M14.8 23.6C16.8 21.4 19.6 20.6 22 21.8" fill="none" stroke="var(--fur)" stroke-width="4.2" stroke-linecap="round"/></g></g></g><g transform="translate(-7 0)"><g class="lb p-lbf"><path d="M30.4 50C26.4 61 29.6 73.6 36.6 78C40.6 80.4 45 78.6 47.2 74C50.2 67.6 50.2 57.6 46.2 49.6Z" fill="var(--fur2)"/><g class="kn"><path d="M35.2 72C34.4 78.4 35.2 82.8 36 86.4C33.6 87.4 33.6 90.6 36.6 90.6H43.8C46.8 90.6 46.6 87 42.6 86.4C42.2 82.2 42.6 78 43.6 72Z" fill="var(--fur2)"/></g></g></g><g transform="translate(-7 0)"><g class="lf p-lff"><path d="M76.4 52C75.6 61 77 69.6 78.8 76H86.6C87.4 68.6 88.4 60.6 88.4 52Z" fill="var(--fur2)"/><g class="kn"><path d="M78.8 74C78.4 79 79.2 83.4 79.8 86.4C77.6 87.2 77.6 90.6 80.6 90.6H87.8C90.8 90.6 90.6 87.2 86.2 86.4C85.8 82.6 86 78.4 86.6 74Z" fill="var(--fur2)"/></g></g></g><g class="p-body"><g class="b-stand"><path d="M91 44.5C81 39.5 63 41.8 48 40.6C34 39.6 22.5 42 19.8 53.5C17.6 63.5 22 73 31 75.6C38.4 77.6 44 72.6 52 70.8C60 69.2 67.6 70.6 74 71C84 71.8 95 68.5 97 59C98 52.6 95.6 47.4 91 44.5Z" fill="var(--fur)"/><path d="M83.6 52.4C88 58 88.4 65.6 84.8 70.8C86.2 65 85.8 58.4 83.6 52.4Z" fill="var(--crease)"/></g><g class="b-sit"><path d="M80 36C90 38 95 50 94 62C93 72 90.6 82 90.4 90.4C76 91.4 56 91.4 42 90.8C32 90.4 27 84 28.4 76C30 66 38 58 46 52C56 45 66 38 80 36Z" fill="var(--fur)"/><path d="M30.4 74C33 64 46 61.6 54.4 68C60 72.6 60 83.6 55.8 88.6C50 90.4 38 90.6 32.8 88.6C29.2 85 29 79 30.4 74Z" fill="var(--fur)"/><path d="M31.6 70.4C38 63.8 50 63.6 55.6 70.4C48.8 66.2 39 66 31.6 70.4Z" fill="var(--crease)"/><path d="M86.6 60C89.6 68 89.4 80 87.6 86.8C88.2 79 87.8 68 86.6 60Z" fill="var(--crease)"/><path d="M56 90.6C52.6 90.6 52.4 86.2 56.6 85.8H64C67.6 85.8 68 90.6 64.2 90.6Z" fill="var(--fur)"/></g><g class="b-loaf"><path d="M16.6 90.4C12.4 77 24.4 63.4 46 61.8C66 60.4 84 65.4 91.4 77.6C94.2 82.6 94 87.6 90.6 90.4C70 91.6 36 91.6 16.6 90.4Z" fill="var(--fur)"/><path d="M24.2 72.4C29.6 66.6 38.6 64.8 46.4 67C38.2 67.2 30.8 69 24.2 72.4Z" fill="var(--crease)"/></g></g><g class="lb p-lbn"><path d="M30.4 50C26.4 61 29.6 73.6 36.6 78C40.6 80.4 45 78.6 47.2 74C50.2 67.6 50.2 57.6 46.2 49.6Z" fill="var(--fur)"/><path d="M46.4 57.6C49.4 65.6 46.6 74.6 39.4 78.6C44.2 74 46.4 66.6 45 58.6Z" fill="var(--crease)"/><g class="kn"><path d="M35.2 72C34.4 78.4 35.2 82.8 36 86.4C33.6 87.4 33.6 90.6 36.6 90.6H43.8C46.8 90.6 46.6 87 42.6 86.4C42.2 82.2 42.6 78 43.6 72Z" fill="var(--fur)"/></g></g><g class="lf p-lfn"><path d="M76.4 52C75.6 61 77 69.6 78.8 76H86.6C87.4 68.6 88.4 60.6 88.4 52Z" fill="var(--fur)"/><g class="kn"><path d="M78.8 74C78.4 79 79.2 83.4 79.8 86.4C77.6 87.2 77.6 90.6 80.6 90.6H87.8C90.8 90.6 90.6 87.2 86.2 86.4C85.8 82.6 86 78.4 86.6 74Z" fill="var(--fur)"/><path d="M78.9 75C78.7 80 79.3 83.6 80 86.2C80.6 83.4 80.4 79.4 80.4 75Z" fill="var(--crease)"/></g></g><path class="tl-curl" d="M22 83.4C34 95 72 95.4 96.4 87.6C98.4 87 99.2 89.4 97.4 90.2C72 100.6 32 100.4 19 89.6C17.4 88 19.6 82.6 22 83.4Z" fill="var(--fur)"/><g class="p-head"><path d="M81.4 29C80.4 21 81.2 13.4 83.6 8.8C84.6 7.2 86.2 7.2 87.4 8.2C90.8 11.2 93.4 15.2 94.8 20ZM112.6 29C113.6 21 112.8 13.4 110.4 8.8C109.4 7.2 107.8 7.2 106.6 8.2C103.2 11.2 100.6 15.2 99.2 20Z" fill="var(--fur)" stroke="var(--fur)" stroke-width="${he ? 3.4 : 2}" stroke-linejoin="round"/><path d="M84.4 22.6C84 17.6 84.6 13.6 85.8 11.6C88.4 13.6 90.4 16.4 91.6 19.6C89.4 20.2 86.6 21.2 84.4 22.6ZM109.6 22.6C110 17.6 109.4 13.6 108.2 11.6C105.6 13.6 103.6 16.4 102.4 19.6C104.6 20.2 107.4 21.2 109.6 22.6Z" fill="var(--inear)"/><path d="M97 16.5C106.6 16.5 113.6 22.4 114.6 30.4C115.1 34.4 114.4 37 113 39.2Q116.8 40.4 117.6 43.4Q114.8 42.8 112.2 43.8C108.6 47.6 103 49.4 97 49.4C91 49.4 85.4 47.6 81.8 43.8Q79.2 42.8 76.4 43.4Q77.2 40.4 81 39.2C79.6 37 78.9 34.4 79.4 30.4C80.4 22.4 87.4 16.5 97 16.5Z" fill="var(--fur)"/><path d="M86 25.6C88.4 24 91.4 23.8 93.4 24.8C91 24.8 88.4 25.2 86 25.6ZM108 25.6C105.6 24 102.6 23.8 100.6 24.8C103 24.8 105.6 25.2 108 25.6Z" fill="var(--crease)"/><ellipse cx="94.5" cy="41.6" rx="3.5" ry="2.7" fill="var(--muz)"/><ellipse cx="99.5" cy="41.6" rx="3.5" ry="2.7" fill="var(--muz)"/><g class="eyes-o"><ellipse cx="90.4" cy="32.6" rx="2.9" ry="3.5" fill="var(--eye)"/><ellipse cx="103.6" cy="32.6" rx="2.9" ry="3.5" fill="var(--eye)"/>${he ? PUP_HE : PUP_SHE}<circle cx="91.4" cy="31.3" r=".9" fill="#fff"/><circle cx="104.6" cy="31.3" r=".9" fill="#fff"/></g><g class="eyes-c"><path d="M87.4 33q3 2.6 6 0M100.6 33q3 2.6 6 0" fill="none" stroke="var(--ink)" stroke-width="1.3" stroke-linecap="round"/></g>${he ? "" : CHEEKS}<path d="M95.3 38.5C95.3 37.6 98.7 37.6 98.7 38.5C98.7 39.3 97.6 40.1 97 40.5C96.4 40.1 95.3 39.3 95.3 38.5Z" fill="var(--nose)"/><path d="M97 40.5v.7q-1.2 1.7-2.9.7M97 41.2q1.2 1.7 2.9.7" fill="none" stroke="var(--ink)" stroke-width=".75" stroke-linecap="round"/><path d="M91.4 40.8Q84 39 76.6 39.8M91.4 42.2Q84.6 42.6 77.8 44.8M102.6 40.8Q110 39 117.4 39.8M102.6 42.2Q109.4 42.6 116.2 44.8" fill="none" stroke="var(--wh)" stroke-width=".6" stroke-linecap="round"/>${he ? COLLAR_HE : COLLAR_SHE}</g></svg>`;

/** A cat: `id` w is she, b is he; `pose` is its class; `face` l or r. */
const kit = (id: string, pose: string, face: string, left = "") =>
  `<div class="kit k${id} ${pose}" data-face="${face}" data-a="${id}"${left && ` style="left:${left}"`}><i class="gs"></i><div class="fx">${rig(id === "b")}</div></div>`;
const XTRA: Record<string, string> = {
  yarn: `<svg viewBox="0 0 24 24" width="24" height="24"><circle cx="12" cy="12" r="10.5" fill="var(--a2)"/><path d="M4 8.6c5 1.4 11 1.4 16-1M3 14c6 1.6 12 .8 17.6-2.6M6.4 19.6c3.6-4.6 6-10 6.6-16.8M11 21.6c2.6-4 4.6-9.8 4.6-17" fill="none" stroke="color-mix(in srgb,var(--a2) 55%,#fff)" stroke-width="1.2" stroke-linecap="round"/></svg>`,
  heart: `<svg viewBox="0 0 24 22" width="22" height="20"><path d="M12 20.4C4.6 15.6 1.6 11.8 1.6 7.6a5.1 5.1 0 0 1 10.4-1.8a5.1 5.1 0 0 1 10.4 1.8c0 4.2-3 8-10.4 12.8z" fill="var(--a2)"/></svg>`,
  zzz: `<span style="font:700 15px var(--font-display),sans-serif;color:var(--t2)">z<span style="font-size:11px;vertical-align:6px"> z</span><span style="font-size:8px;vertical-align:12px"> z</span></span>`,
};

/** One step of a track: time in ms, x (the left edge in the strip) and y in px, opacity, scale, turn, pose, facing.
 * A value left out keeps the last one. */
type Step = { t: number; x?: number; y?: number; o?: number; s?: number; r?: number; p?: string; f?: string };

/** The board's four scenes for a strip `W` px wide (34.2). */
export function scene(n: number, W: number): { dur: number; tracks: Record<string, Step[]> } {
  const C = 120;
  const mid = W / 2;
  if (n === 1)
    return {
      dur: 7000,
      tracks: {
        w: [{ t: 0, x: -C - 10, p: "walk", f: "r" }, { t: 2300, x: mid - 116, p: "stand" }, { t: 2600, p: "boop" }, { t: 4300, p: "stand" }, { t: 4700, p: "walk" }, { t: 7000, x: W + 20 }],
        b: [{ t: 0, x: W + 10, p: "walk", f: "l" }, { t: 2300, x: mid + 12, p: "stand" }, { t: 2650, p: "boop" }, { t: 4300, p: "stand" }, { t: 4500, f: "r" }, { t: 4650, p: "walk" }, { t: 7000, x: W + 170 }],
        heart: [{ t: 0, x: mid - 4, y: 62, o: 0, s: 0.2 }, { t: 2850, o: 0, s: 0.2 }, { t: 3150, y: 66, o: 1, s: 1.15 }, { t: 3400, y: 70, s: 1 }, { t: 4300, y: 96, o: 0 }, { t: 7000 }],
      },
    };
  if (n === 2)
    return {
      dur: 7000,
      tracks: {
        yarn: [{ t: 0, x: -30, r: 0 }, { t: 1600, x: W * 0.38, r: 330 }, { t: 1900, x: W * 0.4, y: 16, r: 350 }, { t: 2200, x: W * 0.43, y: 0, r: 380 }, { t: 2900, x: W * 0.6, r: 560 }, { t: 4200, x: W * 0.66, r: 610 }, { t: 6300, x: W + 40, r: 1100 }, { t: 7000 }],
        b: [{ t: 0, x: -C - 60, p: "run", f: "r" }, { t: 1600, x: W * 0.38 - 116, p: "bat" }, { t: 2500, p: "run" }, { t: 3300, x: W * 0.66 - 116, p: "crouch" }, { t: 4800, p: "run" }, { t: 7000, x: W + 60 }],
        w: [{ t: 0, x: -C - 20, p: "walk", f: "r" }, { t: 1700, x: -C - 20 }, { t: 3900, x: W * 0.66 - 250, p: "crouch" }, { t: 4850, p: "run" }, { t: 7000, x: W - 40 }],
      },
    };
  if (n === 3)
    return {
      dur: 7500,
      tracks: {
        w: [{ t: 0, x: -C - 10, p: "walk", f: "r" }, { t: 2400, x: mid + 10, p: "sit" }, { t: 3500, p: "stretch" }, { t: 4900, p: "sit" }, { t: 5500, p: "walk" }, { t: 7500, x: W + 150 }],
        b: [{ t: 0, x: -C - 150, p: "walk", f: "r" }, { t: 2400, x: mid - 120, p: "sit" }, { t: 3600, p: "stretch" }, { t: 5000, p: "sit" }, { t: 5500, p: "walk" }, { t: 7500, x: W + 20 }],
      },
    };
  return {
    dur: 8000,
    tracks: {
      b: [{ t: 0, x: W + 10, p: "walk", f: "l" }, { t: 1900, x: mid - 6, p: "stand" }, { t: 2200, p: "curl" }, { t: 6000, p: "stand" }, { t: 6150, f: "r" }, { t: 6300, p: "stretch" }, { t: 6900, p: "walk" }, { t: 8000, x: W + 160 }],
      w: [{ t: 0, x: -C - 10, p: "walk", f: "r" }, { t: 1300, x: -C - 10 }, { t: 3700, x: mid - 112, p: "sit" }, { t: 4100, p: "curl" }, { t: 6050, p: "stand" }, { t: 6300, p: "stretch" }, { t: 6900, p: "walk" }, { t: 8000, x: W + 30 }],
      zzz: [{ t: 0, x: mid + 30, y: 40, o: 0 }, { t: 2700, o: 0 }, { t: 3100, y: 48, o: 0.9 }, { t: 5800, y: 78, o: 0.9 }, { t: 6100, y: 84, o: 0 }, { t: 8000 }],
    },
  };
}

let last = 0; // the scene that played last, so the next one differs (memory only)
let started = -Infinity; // when the last one started: at most one per 10 s, and none while one plays
let layer: HTMLElement | null = null;
let timers: ReturnType<typeof setTimeout>[] = [];

/** Ends the scene now: the layer leaves the DOM (its animations with it). */
export function stop() {
  timers.forEach(clearTimeout);
  timers = [];
  layer?.remove();
  layer = null;
  document.removeEventListener("visibilitychange", hidden);
}
const hidden = () => document.hidden && stop();

/** Plays a random scene (never the last one), or with reduced motion the still pair for 2 s. */
export function play() {
  if (document.hidden || Date.now() - started < 10000) return; // hidden: the tab went away while the chunk loaded
  started = Date.now();
  if (!document.getElementById("cats-css"))
    document.head.insertAdjacentHTML("beforeend", `<style id="cats-css">${CSS}</style>`);
  const el = document.createElement("div");
  el.className = "cats";
  el.setAttribute("aria-hidden", "true");
  el.setAttribute("popover", "manual"); // the top layer: above drawers, toasts and dialogs
  const later = (ms: number, fn: () => void) => timers.push(setTimeout(fn, ms));
  const still = !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
  const pick = [1, 2, 3, 4].filter((k) => k !== last);
  const n = still ? 0 : (last = pick[Math.floor(Math.random() * pick.length)]);
  el.dataset.scene = still ? "still" : String(n);
  el.innerHTML = still
    ? `<div class="rm">${kit("b", "wave still", "r", "0")}${kit("w", "sit still", "l", "74px")}</div>`
    : Object.keys(scene(n, 0).tracks)
        .map((id) => (XTRA[id] ? `<div class="xtra" data-a="${id}">${XTRA[id]}</div>` : kit(id, "", "r")))
        .join("");
  document.body.append(el);
  el.showPopover?.();
  layer = el;
  document.addEventListener("visibilitychange", hidden);
  if (still) {
    later(2000, stop);
    return;
  }
  const { dur, tracks } = scene(n, el.clientWidth);
  for (const [id, steps] of Object.entries(tracks)) {
    const a = el.querySelector<HTMLElement>(`[data-a="${id}"]`)!;
    let x = 0, y = 0, o = 1, s = 1, r = 0; // prettier-ignore
    a.animate(
      steps.map((k) => {
        x = k.x ?? x;
        y = k.y ?? y;
        o = k.o ?? o;
        s = k.s ?? s;
        r = k.r ?? r;
        return { offset: k.t / dur, opacity: o, transform: `translate(${x}px,${-y}px) rotate(${r}deg) scale(${s})` };
      }),
      { duration: dur, fill: "forwards" },
    );
    for (const k of steps)
      if (k.p || k.f) {
        const set = () => {
          if (k.p) a.className = `kit k${id} ${k.p}`;
          if (k.f) a.dataset.face = k.f;
        };
        if (k.t) later(k.t, set);
        else set();
      }
  }
  later(dur + 60, stop);
}
