import { useSyncExternalStore } from "react";

const noSubscribe = () => () => {};

/** False in the server render and while hydrating, true after: the viewer's locale and time zone aren't the server's,
 * so a local time is drawn only once this is true. */
export const useHydrated = () =>
  useSyncExternalStore(
    noSubscribe,
    () => true,
    () => false,
  );
