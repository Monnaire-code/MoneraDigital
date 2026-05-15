#!/bin/bash
# Test login for gyc567@gmail.com

echo "Testing login for gyc567@gmail.com..."
echo ""
echo "If login fails, please try one of these options:"
echo ""
echo "1. Reset password at: http://localhost:5001/forgot-password"
echo ""
echo "2. Register a new test account:"
echo "   curl -X POST http://localhost:5001/api/auth/register \\"
echo "     -H 'Content-Type: application/json' \\"
echo "     -d '{\"email\":\"your-email@example.com\",\"password\":\"TestPassword123!\"}'"
echo ""
echo "3. Use the test account I just created:"
echo "   Email: test.user.12345@example.com"
echo "   Password: TestPassword123!"
echo ""

# Test login
echo "Testing login with test account..."
RESPONSE=$(curl -s -X POST http://localhost:5001/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"test.user.12345@example.com","password":"TestPassword123!"}')

echo "Response: $RESPONSE"
echo ""

# Extract token (if successful)
TOKEN=$(echo $RESPONSE | grep -o '"accessToken":"[^"]*"' | cut -d'"' -f4)
if [ -n "$TOKEN" ]; then
  echo "✅ Login successful!"
  echo "Token: ${TOKEN:0:50}..."
else
  echo "❌ Login failed"
fi
