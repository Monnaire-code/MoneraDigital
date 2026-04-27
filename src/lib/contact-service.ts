import { z } from "zod";
import { tokenManager } from "./token-manager.js";
import logger from "./logger.js";

const contactInfoSchema = z.object({
  phone: z.string().min(1, "Phone number is required"),
  telegram: z.string().optional(),
  wechat: z.string().optional(),
});

export interface SubmitContactInfoResponse {
  success: boolean;
  message?: string;
  status?: string;
  redirectUrl?: string;
  reviewDays?: number;
  reviewDate?: string;
  error?: string;
  code?: string;
}

export interface ContactInfoData {
  phone: string;
  telegram?: string;
  wechat?: string;
}

export class ContactService {
  static async submitContactInfo(data: ContactInfoData): Promise<SubmitContactInfoResponse> {
    const validated = contactInfoSchema.parse(data);

    logger.info({ data: { phone: validated.phone, hasTelegram: !!validated.telegram, hasWechat: !!validated.wechat } }, "Submitting contact info");

    const token = tokenManager.getAccessToken();
    if (!token) {
      return {
        success: false,
        error: "Authentication required",
        code: "UNAUTHORIZED",
      };
    }

    const response = await fetch("/api/contact-info", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: `Bearer ${token}`,
      },
      body: JSON.stringify(validated),
    });

    const result = await response.json();

    if (!response.ok) {
      logger.error({ status: response.status, result }, "Failed to submit contact info");
      return {
        success: false,
        error: result.message || "Failed to submit contact information",
        code: result.code,
      };
    }

    logger.info({ result }, "Contact info submitted successfully");
    return result;
  }
}
