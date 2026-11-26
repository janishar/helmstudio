"use client";
// The Magic route's client entry: the board (its editor, the player's engine) loads only here, in the browser, where
// phase B keeps the boards.
import dynamic from "next/dynamic";
import { LoadingBoard } from "@/components/magic/loading";

const Board = dynamic(() => import("@/components/magic/board").then((m) => m.MagicBoard), {
  ssr: false,
  loading: LoadingBoard,
});

export function MagicScreen() {
  return <Board />;
}
