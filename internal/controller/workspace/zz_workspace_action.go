package workspace

import "fmt"

func (w *external) CreateWorkspace() (workspaceId string, err error) {

	scalarClient := w.service.(*ScalrService)
	fmt.Println("Creating workspace with attributes: ", scalarClient)
	fmt.Println("Creating workspace...")
	return "", nil
}

func (w *external) WorkspaceTags(workspaceId string) (err error) {
	fmt.Println("Retrieving workspace tags...")
	return nil
}
