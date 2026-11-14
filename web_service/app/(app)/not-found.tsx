/** An unknown signed-in route, in the quiet style of the later-section placeholder; the dock stays (the [...section] page already titles it "Not found"). */
export default function NotFound() {
  return (
    <main
      id="main"
      className="flex flex-1 flex-col items-center justify-center gap-2 p-6 text-center"
    >
      <h1 className="font-display text-[22px] font-bold">Not found</h1>
      <p className="text-t2">Nothing lives at this address.</p>
    </main>
  );
}
