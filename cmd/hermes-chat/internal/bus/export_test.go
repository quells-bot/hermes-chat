package bus

var HashPassword, VerifyPassword = hashPassword, verifyPassword

// ThrottleEntries reports how many limiters each map holds.
func (s *service) ThrottleEntries() (usernames, ips int) { return s.throttle.entries() }
