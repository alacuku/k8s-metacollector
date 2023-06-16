// Copyright 2023 The Falco Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metadata

import (
	"sync"

	"github.com/go-logr/logr"
)

// Connection used to track a subscriber connection. Each time a subscriber arrives a
// Connection is created and stored for later use by the Broker.
type Connection struct {
	Error    chan error
	Stream   Metadata_WatchServer
	Selector *Selector
}

// Server grpc server started by the broker that listens for new connections from subscribers.
type Server struct {
	UnimplementedMetadataServer
	subscribers   *sync.Map
	logger        logr.Logger
	collectors    map[string]chan<- string
	connectionsWg *sync.WaitGroup
}

// New returns a new Server.
func New(logger logr.Logger, subs *sync.Map, collectors map[string]chan<- string, group *sync.WaitGroup) *Server {
	return &Server{
		subscribers:   subs,
		logger:        logger,
		collectors:    collectors,
		connectionsWg: group,
	}
}

// Watch accepts a Selector and returns a stream of metadata to the client. On each watch it creates a Connection
// for the client and stores it for later use by the broker. On each new watch it triggers the dispatch of existing
// metadata to the subscriber for each watched resource.
func (s *Server) Watch(selector *Selector, stream Metadata_WatchServer) error {
	var err error
	s.logger.Info("received watch request", "subscriber", selector.NodeName)
	errorChan := make(chan error)

	// Check if the client subscribed previously.
	_, ok := s.subscribers.Load(selector.NodeName)
	if ok {
		s.logger.Info("ignoring subscription since a subscriber already exists", "name", selector.NodeName)
		return nil
	}

	s.subscribers.Store(selector.NodeName, Connection{
		Error:    errorChan,
		Stream:   stream,
		Selector: selector,
	})

	s.logger.V(5).Info("starting initial event sync", "subscriber", selector.NodeName)
	for resource, filter := range selector.ResourceKinds {
		s.logger.V(5).Info("dispatching initial sync", "subscriber", selector.NodeName, "resource", resource, "selector", filter)
		if collector, ok := s.collectors[resource]; ok {
			collector <- selector.NodeName
		}
		s.logger.V(5).Info("initial sync correctly dispatched", "subscriber", selector.NodeName, "resource", resource, "selector", filter)
	}

	// Add the connection to waiting group.
	s.connectionsWg.Add(1)
	// At exit time remove the connection from the waiting group.
	defer s.connectionsWg.Done()

	select {
	case <-stream.Context().Done():
		s.logger.Info("context canceled, closing connection", "subscriber", selector.NodeName)
	case err = <-errorChan:
		s.logger.Error(err, "closing connection", "subscriber", selector.NodeName)
	}

	s.subscribers.Delete(selector.NodeName)
	return err
}