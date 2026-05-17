output "cluster_names" {
  description = "Names of all Kubernetes clusters"
  value       = { for k, v in yandex_kubernetes_cluster.k8s-cluster : k => v.name }
}

output "get_credentials_cmds" {
  description = "Commands to fetch kubeconfig for each cluster"
  value = {
    for k, v in yandex_kubernetes_cluster.k8s-cluster :
    k => "yc managed-kubernetes cluster get-credentials ${v.name} --external --force --kubeconfig ~/.kube/${k}.yaml"
  }
}

output "runner_ip" {
  description = "Public IP of the obs-bench runner VM"
  value       = yandex_compute_instance.runner.network_interface[0].nat_ip_address
}

output "runner_ssh_cmd" {
  description = "SSH command to connect to the runner VM"
  value       = "ssh ubuntu@${yandex_compute_instance.runner.network_interface[0].nat_ip_address}"
}
