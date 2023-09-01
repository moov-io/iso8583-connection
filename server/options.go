package server

// WithConnectionFactory is a functional option that allows to set custom
// connection factory function. This function is used to create new connection
// with custom options (e.g. custom message length reader/writer)
func WithConnectionFactory(f ServerConnectionFactoryFunc) func(*Server) error {
	return func(s *Server) error {
		s.connectionFactory = f

		return nil
	}
}
