import { useState, useEffect } from "react";
import { useTranslation } from "react-i18next";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Badge } from "@/components/ui/badge";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Plus, Trash2, Check, Clock, AlertCircle, Shield, Lock } from "lucide-react";
import { Link } from "react-router-dom";
import { toast } from "sonner";

interface WithdrawalAddress {
  id: number;
  walletAddress: string;
  chainType: "BTC" | "ETH" | "USDC" | "USDT";
  addressAlias: string;
  verified: boolean;
  isDeleted: boolean;
  isPrimary: boolean;
  createdAt: string;
  verifiedAt: string | null;
}

const Addresses = () => {
  const { t } = useTranslation();
  const [addresses, setAddresses] = useState<WithdrawalAddress[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [isDialogOpen, setIsDialogOpen] = useState(false);
  const [isVerifyDialogOpen, setIsVerifyDialogOpen] = useState(false);
  const [selectedAddressId, setSelectedAddressId] = useState<number | null>(null);

  // Form states
  const [newAddress, setNewAddress] = useState("");
  const [addressType, setAddressType] = useState<"ETH" | "BTC" | "USDC" | "USDT">("ETH");
  const [label, setLabel] = useState("");
  const [verificationToken, setVerificationToken] = useState("");
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deleteAddressId, setDeleteAddressId] = useState<number | null>(null);
  const [userTwoFactorEnabled, setUserTwoFactorEnabled] = useState(false);

  // Fetch addresses
  const fetchAddresses = async () => {
    try {
      const token = localStorage.getItem("token");
      const res = await fetch("/api/addresses", {
        headers: { Authorization: `Bearer ${token}` },
      });

      if (res.ok) {
        const data = await res.json();
        setAddresses(data.addresses || []);
      } else {
        toast.error(t("addresses.failedToFetch"));
      }
    } catch (error) {
      console.error("Fetch addresses error:", error);
      toast.error(t("addresses.failedToFetch"));
    } finally {
      setIsLoading(false);
    }
  };

  useEffect(() => {
    fetchAddresses();
    
    // Fetch user profile to check 2FA status
    const fetchProfile = async () => {
      try {
        const token = localStorage.getItem("token");
        const res = await fetch("/api/auth/me", {
          headers: { Authorization: `Bearer ${token}` }
        });
        if (res.ok) {
          const data = await res.json();
          // Check for snake_case or camelCase just in case
          setUserTwoFactorEnabled(!!(data.two_factor_enabled || data.twoFactorEnabled));
        }
      } catch (e) {
        console.error("Failed to fetch user profile", e);
      }
    };
    fetchProfile();
  }, []);

  // Add new address
  const handleAddAddress = async () => {
    if (!newAddress || !label) {
      toast.error(t("addresses.pleaseFillAllFields"));
      return;
    }

    setIsSubmitting(true);
    try {
      const token = localStorage.getItem("token");
      const res = await fetch("/api/addresses", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          wallet_address: newAddress,
          chain_type: addressType,
          address_alias: label,
        }),
      });

      const data = await res.json();
      if (res.ok) {
        const newAddressId = data.id;
        
        // If user has 2FA enabled, guide them to verify with 2FA
        if (userTwoFactorEnabled) {
          toast.success(t("addresses.addSuccessMessage"));
          setNewAddress("");
          setLabel("");
          setAddressType("ETH");
          // Keep dialog open and guide user to verify with 2FA
          setSelectedAddressId(newAddressId);
          setIsVerifyDialogOpen(true);
          await fetchAddresses();
        } else {
          // No 2FA - address added successfully (may still need email verification)
          toast.success(data.message || t("addresses.addSuccessMessage"));
          setNewAddress("");
          setLabel("");
          setAddressType("ETH");
          setIsDialogOpen(false);
          await fetchAddresses();
        }
      } else {
        if (res.status === 409) {
          toast.error(t("addresses.duplicateError"));
        } else {
          toast.error(data.error || t("addresses.failedToAdd"));
        }
      }
    } catch (error) {
      console.error("Add address error:", error);
      toast.error(t("addresses.failedToAdd"));
    } finally {
      setIsSubmitting(false);
    }
  };

  // Verify address
  const handleVerifyAddress = async () => {
    if (!verificationToken || !selectedAddressId) {
      toast.error(t("addresses.enterVerificationToken"));
      return;
    }

    setIsSubmitting(true);
    try {
      const token = localStorage.getItem("token");
      const res = await fetch(`/api/addresses/${selectedAddressId}/verify`, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({ token: verificationToken }),
      });

      const data = await res.json();
      if (res.ok) {
        toast.success(data.message || t("addresses.verifySuccess"));
        setVerificationToken("");
        setIsVerifyDialogOpen(false);
        await fetchAddresses();
      } else {
        toast.error(data.error || t("addresses.failedToVerify"));
      }
    } catch (error) {
      console.error("Verify address error:", error);
      toast.error(t("addresses.failedToVerify"));
    } finally {
      setIsSubmitting(false);
    }
  };

  // Set as primary
  const handleSetPrimary = async (addressId: number) => {
    try {
      const token = localStorage.getItem("token");
      const res = await fetch(`/api/addresses/${addressId}/primary`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });

      if (res.ok) {
        toast.success(t("addresses.primarySetSuccess"));
        await fetchAddresses();
      } else {
        toast.error(t("addresses.failedToSetPrimary"));
      }
    } catch (error) {
      console.error("Set primary error:", error);
      toast.error(t("addresses.failedToSetPrimary"));
    }
  };

  // Delete/Deactivate address
  const handleDeleteAddress = async () => {
    if (!deleteAddressId) return;

    try {
      const token = localStorage.getItem("token");
      const res = await fetch(`/api/addresses/${deleteAddressId}`, {
        method: "DELETE",
        headers: { Authorization: `Bearer ${token}` },
      });

      const data = await res.json();
      if (res.ok) {
        toast.success(data.message || t("addresses.deactivatedSuccess"));
        setDeleteAddressId(null);
        await fetchAddresses();
      } else {
        toast.error(data.error || t("addresses.failedToDeactivate"));
      }
    } catch (error) {
      console.error("Delete address error:", error);
      toast.error(t("addresses.failedToDeactivate"));
    }
  };

  return (
    <div className="space-y-8 animate-fade-in">
      <div className="flex items-center justify-between">
        <h1 className="text-3xl font-bold tracking-tight">{t("dashboard.addresses.title")}</h1>
        <Button onClick={() => setIsDialogOpen(true)} className="gap-2">
          <Plus size={20} />
          {t("dashboard.addresses.addAddress")}
        </Button>
      </div>

      {isLoading ? (
        <Card className="bg-card/50 border-border/50">
          <CardContent className="pt-6">
            <div className="text-center text-muted-foreground">{t("dashboard.addresses.loading")}</div>
          </CardContent>
        </Card>
      ) : addresses.length === 0 ? (
        <Card className="bg-card/50 border-border/50">
          <CardContent className="pt-6">
            <div className="text-center text-muted-foreground">{t("dashboard.addresses.noAddresses")}</div>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4">
          {addresses.map((addr) => (
            <Card key={addr.id} className="bg-card/50 border-border/50">
              <CardContent className="pt-6">
                <div className="flex items-start justify-between gap-4">
                  <div className="flex-1 space-y-3">
                    <div className="flex items-center gap-3">
                      <h3 className="font-semibold">{addr.addressAlias}</h3>
                      <Badge variant={addr.chainType === "BTC" ? "default" : "secondary"}>
                        {addr.chainType}
                      </Badge>
                      {addr.verified && (
                        <Badge variant="outline" className="gap-1">
                          <Check size={14} />
                          {t("dashboard.addresses.verified")}
                        </Badge>
                      )}
                      {!addr.verified && (
                        <Badge variant="outline" className="gap-1 text-yellow-600">
                          <Clock size={14} />
                          {t("dashboard.addresses.pending")}
                        </Badge>
                      )}
                      {/* addr.isPrimary not supported yet */}
                      {addr.isDeleted && (
                        <Badge variant="destructive">{t("dashboard.addresses.deactivated")}</Badge>
                      )}
                    </div>

                    <div className="space-y-2">
                      <p className="text-sm text-muted-foreground break-all font-mono">
                        {addr.walletAddress}
                      </p>
                      <p className="text-xs text-muted-foreground">
                        {t("dashboard.addresses.added")}: {new Date(addr.createdAt).toLocaleDateString()}
                      </p>
                      {addr.verifiedAt && (
                        <p className="text-xs text-green-600">
                          {t("dashboard.addresses.verified")}: {new Date(addr.verifiedAt).toLocaleDateString()}
                        </p>
                      )}
                    </div>
                  </div>

                  <div className="flex gap-2">
                    {!addr.verified && !addr.isDeleted && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => {
                          setSelectedAddressId(addr.id);
                          setIsVerifyDialogOpen(true);
                        }}
                      >
                        {t("dashboard.addresses.verify")}
                      </Button>
                    )}

                    {/* Primary button logic might need revisit if backend doesn't support it in list */}
                    {addr.verified && !addr.isDeleted && (
                      <Button
                        size="sm"
                        variant="outline"
                        onClick={() => handleSetPrimary(addr.id)}
                      >
                        {t("dashboard.addresses.setPrimary")}
                      </Button>
                    )}

                    {!addr.isDeleted && (
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => setDeleteAddressId(addr.id)}
                        className="text-destructive hover:text-destructive"
                      >
                        <Trash2 size={16} />
                      </Button>
                    )}
                  </div>
                </div>
              </CardContent>
            </Card>
          ))}
        </div>
      )}

      {/* Add Address Dialog */}
      <Dialog open={isDialogOpen} onOpenChange={setIsDialogOpen}>
        <DialogContent className="sm:max-w-[425px]">
          <DialogHeader>
            <DialogTitle>{t("dashboard.addresses.addDialogTitle")}</DialogTitle>
            <DialogDescription>
              {t("dashboard.addresses.addDialogDescription")}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="label">{t("dashboard.addresses.addressLabel")}</Label>
              <Input
                id="label"
                placeholder={t("dashboard.addresses.addressLabelPlaceholder")}
                value={label}
                onChange={(e) => setLabel(e.target.value)}
              />
            </div>

            <div className="space-y-2">
              <Label htmlFor="type">{t("dashboard.addresses.assetType")}</Label>
              <Select value={addressType} onValueChange={(value: any) => setAddressType(value)}>
                <SelectTrigger id="type">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="BTC">{t("dashboard.addresses.btc")}</SelectItem>
                  <SelectItem value="ETH">{t("dashboard.addresses.eth")}</SelectItem>
                  <SelectItem value="USDC">{t("dashboard.addresses.usdc")}</SelectItem>
                  <SelectItem value="USDT">{t("dashboard.addresses.usdt")}</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="space-y-2">
              <Label htmlFor="address">{t("dashboard.addresses.walletAddress")}</Label>
              <Input
                id="address"
                placeholder="0x1234567890abcdef1234567890abcdef12345678"
                value={newAddress}
                onChange={(e) => setNewAddress(e.target.value)}
              />
              <p className="text-xs text-muted-foreground">
                {addressType === "BTC" ? t("dashboard.addresses.btcAddressHint") : t("dashboard.addresses.ethAddressHint")}
              </p>
            </div>
          </div>

          {!userTwoFactorEnabled && (
            <div className="bg-yellow-50 border border-yellow-200 rounded-md p-3 mb-4">
              <div className="flex items-start gap-2">
                <Lock className="h-4 w-4 text-yellow-600 mt-0.5" />
                <div className="text-sm text-yellow-800">
                  <p className="font-medium">{t("dashboard.addresses.2faRequired") || "2FA Required"}</p>
                  <p className="text-xs mt-1">
                    {t("dashboard.addresses.2faRequiredDesc") || "Please enable 2FA to add addresses. "}
                    <Link to="/dashboard/security" className="underline font-medium hover:text-yellow-900">
                      {t("dashboard.addresses.setup2FA") || "Setup 2FA"}
                    </Link>
                  </p>
                </div>
              </div>
            </div>
          )}

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setIsDialogOpen(false)}
              disabled={isSubmitting}
            >
              {t("common.cancel")}
            </Button>
            <Button
              onClick={handleAddAddress}
              disabled={isSubmitting || !newAddress || !label || !userTwoFactorEnabled}
            >
              {isSubmitting ? t("dashboard.addresses.adding") : t("dashboard.addresses.addAddress")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Verify Address Dialog */}
      <Dialog open={isVerifyDialogOpen} onOpenChange={setIsVerifyDialogOpen}>
        <DialogContent className="sm:max-w-[425px]">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {userTwoFactorEnabled && <Shield className="h-5 w-5 text-yellow-500" />}
              {userTwoFactorEnabled 
                ? t("auth.2fa.required") || "2FA Verification" 
                : t("dashboard.addresses.verifyDialogTitle")}
            </DialogTitle>
            <DialogDescription>
              {userTwoFactorEnabled 
                ? t("auth.2fa.enterCode") || "Please enter the 6-digit code from your authenticator app."
                : t("dashboard.addresses.verifyDialogDescription")}
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            <div className="space-y-2">
              <Label htmlFor="token">
                {userTwoFactorEnabled 
                  ? t("auth.2fa.code") || "Verification Code"
                  : t("dashboard.addresses.verificationCode")}
              </Label>
              <Input
                id="token"
                placeholder={userTwoFactorEnabled ? "000000" : t("dashboard.addresses.verificationCodePlaceholder")}
                value={verificationToken}
                onChange={(e) => setVerificationToken(e.target.value)}
                maxLength={userTwoFactorEnabled ? 6 : undefined}
              />
              {userTwoFactorEnabled && (
                <p className="text-xs text-muted-foreground">
                  {t("auth.2fa.hint") || "Enter the code from your Google Authenticator app"}
                </p>
              )}
            </div>
          </div>

          <DialogFooter>
            <Button
              variant="outline"
              onClick={() => setIsVerifyDialogOpen(false)}
              disabled={isSubmitting}
            >
              {t("common.cancel")}
            </Button>
            <Button
              onClick={handleVerifyAddress}
              disabled={isSubmitting || !verificationToken}
            >
              {isSubmitting ? t("dashboard.addresses.verifying") : t("dashboard.addresses.verify")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation Dialog */}
      <AlertDialog open={deleteAddressId !== null} onOpenChange={(open) => !open && setDeleteAddressId(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("dashboard.addresses.deactivateDialogTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("dashboard.addresses.deactivateDialogDescription")}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("common.cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={handleDeleteAddress}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {t("dashboard.addresses.deactivate")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
};

export default Addresses;
