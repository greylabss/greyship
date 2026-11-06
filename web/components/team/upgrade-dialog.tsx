"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from "@/components/primitives/dialog";
import { ToggleGroup, ToggleGroupItem } from "@/components/primitives/toggle-group";
import { Button } from "@/components/ui/button";
import { icons } from "@/components/ui/icons";

export function UpgradeDialog({ variant = "primary" }: { variant?: "primary" | "secondary" }) {
  const [provider, setProvider] = useState("paystack");
  const [open, setOpen] = useState(false);
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button variant={variant} className="self-start">
          Upgrade to Pro
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Upgrade to Pro</DialogTitle>
          <DialogDescription>$8 a month, with $8 of usage included. Pay as you go after that, capped by your spend cap.</DialogDescription>
        </DialogHeader>
        <ul className="flex flex-col gap-2 text-14">
          {["Up to 1 GB per instance, 10 instances", "pprof flame graphs and alerts", "Preview deploys for every pull request", "7-day log retention"].map((i) => (
            <li key={i} className="flex items-center gap-2">
              <icons.check className="text-ok" />
              {i}
            </li>
          ))}
        </ul>
        <div className="flex flex-col gap-1.5">
          <span className="text-13 font-medium">Pay with</span>
          <ToggleGroup type="single" value={provider} onValueChange={(v) => v && setProvider(v)} className="grid w-full grid-cols-2 rounded-input border border-border-strong p-0.5">
            <ToggleGroupItem value="paystack" className="h-9 text-13">
              Naira · Paystack
            </ToggleGroupItem>
            <ToggleGroupItem value="stripe" className="h-9 text-13">
              Dollars · Stripe
            </ToggleGroupItem>
          </ToggleGroup>
        </div>
        <DialogFooter>
          <Button onClick={() => setOpen(false)}>Not now</Button>
          <Button
            variant="primary"
            onClick={() => {
              setOpen(false);
              toast.success("Welcome to Pro", { description: `Billed through ${provider === "paystack" ? "Paystack in naira" : "Stripe in dollars"}` });
            }}
          >
            Continue to {provider === "paystack" ? "Paystack" : "Stripe"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
