import { describe, it, expect, vi, beforeEach } from 'vitest';
// Set environment variable for JWT secret
process.env.JWT_SECRET = 'test-secret';

// Mock fetch globally before importing the service
const mockResponse = {
  ok: true,
  json: vi.fn().mockResolvedValue({}),
};
global.fetch = vi.fn().mockResolvedValue(mockResponse);

import { AuthService } from './auth-service';
import bcrypt from 'bcryptjs';
import jwt from 'jsonwebtoken';

// Mock db module - db module doesn't exist in frontend, so we mock it completely
const mockDb = {
  insert: vi.fn(),
  select: vi.fn(),
  update: vi.fn(),
  delete: vi.fn(),
};

vi.mock('./db', () => ({
  default: mockDb,
  db: mockDb,
}));

vi.mock('jsonwebtoken');

describe('AuthService', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  describe('register', () => {
    it('should successfully register a user', async () => {
      const mockUser = { id: 1, email: 'test@example.com' };
      vi.spyOn(bcrypt, 'hash').mockResolvedValue('hashed_password' as never);

      mockDb.insert.mockReturnValue({
        values: vi.fn().mockReturnValue({
          returning: vi.fn().mockResolvedValue([mockUser]),
        }),
      });

      const result = await AuthService.register('test@example.com', 'password123');

      expect(result).toEqual(mockUser);
      expect(bcrypt.hash).toHaveBeenCalledWith('password123', 10);
    });

    it('should throw error if email or password missing', async () => {
      await expect(AuthService.register('', '')).rejects.toThrow();
    });

    it('should throw error for invalid email format', async () => {
      await expect(AuthService.register('invalid-email', 'password123')).rejects.toThrow('Invalid email format');
    });

    it('should throw error for short password', async () => {
      await expect(AuthService.register('test@example.com', '123')).rejects.toThrow('Password must be at least 8 characters');
    });

    it('should throw error if user already exists', async () => {
      vi.spyOn(bcrypt, 'hash').mockResolvedValue('hashed_password' as never);
      mockDb.insert.mockReturnValue({
        values: vi.fn().mockReturnValue({
          returning: vi.fn().mockRejectedValue({ code: '23505' }),
        }),
      });

      await expect(AuthService.register('test@example.com', 'password123')).rejects.toThrow('User already exists');
    });

    it('should rethrow unknown errors', async () => {
      vi.spyOn(bcrypt, 'hash').mockResolvedValue('hashed_password' as never);
      mockDb.insert.mockReturnValue({
        values: vi.fn().mockReturnValue({
          returning: vi.fn().mockRejectedValue(new Error('DB Error')),
        }),
      });
      await expect(AuthService.register('test@example.com', 'password123')).rejects.toThrow('DB Error');
    });
  });

  describe('login', () => {
    it('should successfully login a user and return a token', async () => {
      const mockUser = { id: 1, email: 'test@example.com', password: 'hashed_password', twoFactorEnabled: false };
      mockDb.select.mockReturnValue({
        from: vi.fn().mockReturnValue({
          where: vi.fn().mockResolvedValue([mockUser]),
        }),
      });

      vi.spyOn(bcrypt, 'compare').mockResolvedValue(true as never);
      (jwt.sign as any).mockReturnValue('mock_token');

      const result = await AuthService.login('test@example.com', 'password123');

      expect(result.token).toBe('mock_token');
      expect(result.user.email).toBe('test@example.com');
    });

    it('should throw error if user not found', async () => {
      mockDb.select.mockReturnValue({
        from: vi.fn().mockReturnValue({
          where: vi.fn().mockResolvedValue([]),
        }),
      });
      await expect(AuthService.login('test@example.com', 'password123')).rejects.toThrow('Invalid email or password');
    });

    it('should throw error if password incorrect', async () => {
      const mockUser = { id: 1, email: 'test@example.com', password: 'hashed_password' };
      mockDb.select.mockReturnValue({
        from: vi.fn().mockReturnValue({
          where: vi.fn().mockResolvedValue([mockUser]),
        }),
      });
      vi.spyOn(bcrypt, 'compare').mockResolvedValue(false as never);
      await expect(AuthService.login('test@example.com', 'password123')).rejects.toThrow('Invalid email or password');
    });

    it('should throw error if email or password missing', async () => {
      await expect(AuthService.login('', '')).rejects.toThrow();
    });
  });
});
