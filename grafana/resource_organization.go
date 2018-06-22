package grafana

import (
	"errors"
	"fmt"
	"github.com/hashicorp/terraform/helper/schema"
	gapi "github.com/mlclmj/go-grafana-api"
	"log"
	"strconv"
	"strings"
)

func ResourceOrganization() *schema.Resource {
	return &schema.Resource{
		Create: CreateOrganization,
		Read:   ReadOrganization,
		Update: UpdateOrganization,
		Delete: DeleteOrganization,
		Exists: ExistsOrganization,

		Schema: map[string]*schema.Schema{
			"name": &schema.Schema{
				Type:        schema.TypeString,
				Required:    true,
				Description: "The name of the Grafana organization.",
			},
			"admin_user": &schema.Schema{
				Type:     schema.TypeString,
				Optional: true,
				Default:  "admin",
				Description: `The name of the Grafana admin user, defaulting to
"admin". Grafana adds this user to all organizations automatically, and this
will keep Terraform from removing them from managed organizations. Specifying a
blank string here will cause Terraform to remove the default admin user from the
organization.`,
			},
			"org_id": &schema.Schema{
				Type:     schema.TypeInt,
				Computed: true,
			},
			"admins": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Description: `A list containing email addresses of users who
should be given the role 'Admin' within this organization. Note: users specified
here must already exist in Grafana.`,
			},
			"editors": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Description: `A list containing email addresses of users who
should have the role 'Editor' within this organization. Note: users specified
here must already exist in Grafana.`,
			},
			"viewers": &schema.Schema{
				Type:     schema.TypeList,
				Optional: true,
				Elem: &schema.Schema{
					Type: schema.TypeString,
				},
				Description: `A list containing email addresses of users who
should have the role 'Viewer' within this organization. Note: users specified
here must already exist in Grafana.`,
			},
		},
	}
}

func CreateOrganization(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*gapi.Client)
	name := d.Get("name").(string)
	err := client.NewOrg(name)
	if err != nil && err.Error() == "409 Conflict" {
		return errors.New(fmt.Sprintf("Error: A Grafana Organization with the name '%s' already exists.", name))
	}
	if err != nil {
		log.Printf("[ERROR] creating Grafana organization %s", name)
		return err
	}
	resp, err := client.OrgByName(name)
	if err != nil {
		return err
	}
	d.SetId(strconv.FormatInt(resp.Id, 10))
	return CreateUsers(d, meta)
}

func ReadOrganization(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*gapi.Client)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	resp, err := client.Org(orgId)
	if err != nil {
		d.SetId("")
		return err
	}
	d.Set("name", resp.Name)
	if err := ReadUsers(d, meta); err != nil {
		return err
	}
	return nil
}

func UpdateOrganization(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*gapi.Client)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	if d.HasChange("name") {
		oldName, newName := d.GetChange("name")
		log.Printf("[ERROR] org name has been updated from %s to %s", oldName.(string), newName.(string))
		name := d.Get("name").(string)
		err := client.UpdateOrg(orgId, name)
		if err != nil {
			return err
		}
	}
	return UpdateUsers(d, meta)
}

func DeleteOrganization(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*gapi.Client)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	return client.DeleteOrg(orgId)
}

func ExistsOrganization(d *schema.ResourceData, meta interface{}) (bool, error) {
	client := meta.(*gapi.Client)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	_, err := client.Org(orgId)
	if err != nil && err.Error() == "404 Not Found" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, err
}

func CreateUsers(d *schema.ResourceData, meta interface{}) error {
	_, newUsers := collectUsers(d)
	userMap, err := userMap(meta)
	if err != nil {
		return err
	}
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	return addUsers(meta, orgId, newUsers, userMap)
}

func ReadUsers(d *schema.ResourceData, meta interface{}) error {
	client := meta.(*gapi.Client)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	orgUsers, err := client.OrgUsers(orgId)
	if err != nil {
		return err
	}
	roleMap := map[string][]string{"Admin": nil, "Editor": nil, "Viewer": nil}
	grafAdmin := d.Get("admin_user")
	for _, orgUser := range orgUsers {
		if orgUser.Login != grafAdmin {
			roleMap[orgUser.Role] = append(roleMap[orgUser.Role], orgUser.Email)
		}
	}
	for k, v := range roleMap {
		d.Set(fmt.Sprintf("%ss", strings.ToLower(k)), v)
	}
	return nil
}

func UpdateUsers(d *schema.ResourceData, meta interface{}) error {
	oldUsers, newUsers := collectUsers(d)
	add, update, remove := userDiff(oldUsers, newUsers)
	orgId, _ := strconv.ParseInt(d.Id(), 10, 64)
	userMap, err := userMap(meta)
	if err != nil {
		return err
	}
	addUsers(meta, orgId, add, userMap)
	updateUsers(meta, orgId, update, userMap)
	removeUsers(meta, orgId, remove, userMap)
	return nil
}

func userMap(meta interface{}) (map[string]int64, error) {
	client := meta.(*gapi.Client)
	userMap := make(map[string]int64)
	users, err := client.Users()
	if err != nil {
		return userMap, err
	}
	for _, user := range users {
		userMap[user.Email] = user.Id
	}
	return userMap, nil
}

func collectUsers(d *schema.ResourceData) (map[string]string, map[string]string) {
	roles := []string{"admins", "editors", "viewers"}
	oldUsers, newUsers := make(map[string]string), make(map[string]string)
	for _, role := range roles {
		roleName := strings.Title(role[:len(role)-1])
		old, new := d.GetChange(role)
		for _, u := range old.([]interface{}) {
			oldUsers[u.(string)] = roleName
		}
		for _, u := range new.([]interface{}) {
			newUsers[u.(string)] = roleName
		}
	}
	return oldUsers, newUsers
}

func userDiff(oldUsers, newUsers map[string]string) (map[string]string, map[string]string, []string) {
	add, update, remove := make(map[string]string), make(map[string]string), []string{}
	for user, role := range newUsers {
		oldRole, ok := oldUsers[user]
		if !ok {
			add[user] = role
			continue
		}
		if oldRole != role {
			update[user] = role
		}
	}
	for user, _ := range oldUsers {
		if _, ok := newUsers[user]; !ok {
			remove = append(remove, user)
		}
	}
	return add, update, remove
}

func addUsers(meta interface{}, orgId int64, users map[string]string, userMap map[string]int64) error {
	client := meta.(*gapi.Client)
	for user, role := range users {
		if _, ok := userMap[user]; !ok {
			log.Printf("[WARN] Skipping adding user '%s'. User is not known to Grafana.", user)
			continue
		}
		if err := client.AddOrgUser(orgId, user, role); err != nil {
			return err
		}
	}
	return nil
}

func updateUsers(meta interface{}, orgId int64, users map[string]string, userMap map[string]int64) error {
	client := meta.(*gapi.Client)
	for user, role := range users {
		userId, ok := userMap[user]
		if !ok {
			log.Printf("[WARN] Skipping updating user '%s'. User is not known to Grafana.", user)
			continue
		}
		if err := client.UpdateOrgUser(orgId, userId, role); err != nil {
			return err
		}
	}
	return nil
}

func removeUsers(meta interface{}, orgId int64, users []string, userMap map[string]int64) error {
	client := meta.(*gapi.Client)
	for _, user := range users {
		userId, ok := userMap[user]
		if !ok {
			log.Printf("[WARN] Skipping deleting user '%s'. User is not known to Grafana.", user)
			continue
		}
		if err := client.RemoveOrgUser(orgId, userId); err != nil {
			return err
		}
	}
	return nil
}
