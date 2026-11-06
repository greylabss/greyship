import { redirect } from "next/navigation";

// A self-hosted Greyship opens straight into the dashboard; /continue sends signed-out visitors to sign in.
export default function Home(): never {
  redirect("/continue");
}
